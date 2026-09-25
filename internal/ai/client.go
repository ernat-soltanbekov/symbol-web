package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

var (
	ErrUnavailable     = errors.New("LLM-сервис недоступен")
	ErrInvalidResponse = errors.New("LLM вернул некорректный ответ")
)

const (
	requestTimeout  = 10 * time.Second
	retryDelay      = 100 * time.Millisecond
	maxResponseSize = 1 << 20
)

// Client supports Ollama and OpenAI-compatible chat completion endpoints.
// A Client can be shared by concurrent HTTP handlers.
type Client struct {
	baseURL string
	model   string
	apiKey  string
	http    *http.Client
	cache   suggestionCache
}

// NewClient creates an LLM client. An empty baseURL selects deterministic mock mode.
func NewClient(baseURL, model string) *Client {
	return &Client{
		baseURL: strings.TrimSpace(baseURL),
		model:   strings.TrimSpace(model),
		apiKey:  strings.TrimSpace(os.Getenv("LLM_API_KEY")),
		http: &http.Client{
			Timeout: requestTimeout,
			// Do not send prompts or credentials to a redirect destination.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// NewClientFromEnv reads configuration once at startup.
func NewClientFromEnv() *Client {
	return NewClient(os.Getenv("LLM_BASE_URL"), os.Getenv("LLM_MODEL"))
}

// MockMode reports whether the client works without an external LLM backend.
func (c *Client) MockMode() bool {
	return c.baseURL == ""
}

// Suggestions returns three to five printable ASCII completions under 50 characters.
func (c *Client) Suggestions(ctx context.Context, text string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if c.MockMode() {
		return mockSuggestions(text), nil
	}
	if suggestions, ok := c.cache.get(text, time.Now()); ok {
		return suggestions, nil
	}
	content, err := c.complete(ctx, suggestionPrompt(text))
	if err != nil {
		return nil, err
	}
	suggestions, err := parseSuggestions(content)
	if err != nil {
		return nil, err
	}
	c.cache.put(text, suggestions, time.Now())
	return suggestions, nil
}

// Variations returns creative text variants with descriptions and banner choices.
func (c *Client) Variations(ctx context.Context, text string) ([]Variation, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if c.MockMode() {
		return mockVariations(text), nil
	}
	content, err := c.complete(ctx, variationPrompt(text))
	if err != nil {
		return nil, err
	}
	return parseVariations(content)
}

func (c *Client) complete(ctx context.Context, prompt string) (string, error) {
	// Retries and response reads share one deadline; retries cannot double the timeout.
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	if c.model == "" {
		return "", fmt.Errorf("%w: переменная LLM_MODEL не задана", ErrUnavailable)
	}
	endpoint, openAI, err := c.endpoint()
	if err != nil {
		return "", err
	}

	var payload any
	if openAI {
		payload = struct {
			Model    string              `json:"model"`
			Messages []map[string]string `json:"messages"`
			Stream   bool                `json:"stream"`
		}{c.model, []map[string]string{{"role": "user", "content": prompt}}, false}
	} else {
		payload = struct {
			Model  string `json:"model"`
			Prompt string `json:"prompt"`
			Stream bool   `json:"stream"`
		}{c.model, prompt, false}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("формирование LLM-запроса: %w", err)
	}

	for attempt := 0; ; attempt++ {
		content, retryable, err := c.request(ctx, endpoint, body, openAI)
		if err == nil || !retryable || attempt == 1 {
			return content, err
		}
		timer := time.NewTimer(retryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", fmt.Errorf("%w: %w", ErrUnavailable, ctx.Err())
		case <-timer.C:
		}
	}
}

func (c *Client) request(ctx context.Context, endpoint string, body []byte, openAI bool) (string, bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", false, fmt.Errorf("%w: невозможно создать запрос", ErrUnavailable)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return "", ctx.Err() == nil, networkFailure(ctx, err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		retryable := response.StatusCode == http.StatusTooManyRequests ||
			response.StatusCode == http.StatusInternalServerError ||
			response.StatusCode == http.StatusBadGateway ||
			response.StatusCode == http.StatusServiceUnavailable ||
			response.StatusCode == http.StatusGatewayTimeout
		return "", retryable, fmt.Errorf("%w: HTTP %d", ErrUnavailable, response.StatusCode)
	}

	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxResponseSize+1))
	if err != nil {
		return "", ctx.Err() == nil, networkFailure(ctx, err)
	}
	if len(responseBody) > maxResponseSize {
		return "", false, fmt.Errorf("%w: ответ слишком большой", ErrInvalidResponse)
	}
	if openAI {
		var payload struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(responseBody, &payload); err != nil || len(payload.Choices) == 0 || strings.TrimSpace(payload.Choices[0].Message.Content) == "" {
			return "", false, fmt.Errorf("%w: OpenAI-ответ не содержит текста", ErrInvalidResponse)
		}
		return payload.Choices[0].Message.Content, false, nil
	}
	var payload struct {
		Response string `json:"response"`
		Error    string `json:"error"`
		Done     *bool  `json:"done"`
	}
	if err := json.Unmarshal(responseBody, &payload); err != nil || payload.Error != "" || strings.TrimSpace(payload.Response) == "" || (payload.Done != nil && !*payload.Done) {
		return "", false, fmt.Errorf("%w: Ollama-ответ не содержит завершённого текста", ErrInvalidResponse)
	}
	return payload.Response, false, nil
}

// Transport errors can contain the backend URL or credentials; log only their category.
func networkFailure(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, ctx.Err())
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %w", ErrUnavailable, context.DeadlineExceeded)
	}
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("%w: %w", ErrUnavailable, context.Canceled)
	}
	return fmt.Errorf("%w: сбой сетевого запроса", ErrUnavailable)
}

func (c *Client) endpoint() (string, bool, error) {
	parsed, err := url.Parse(c.baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return "", false, fmt.Errorf("%w: неверный LLM_BASE_URL", ErrUnavailable)
	}
	path := strings.TrimRight(parsed.Path, "/")
	openAI := false
	switch {
	case strings.HasSuffix(path, "/chat/completions"):
		openAI = true
	case strings.HasSuffix(path, "/v1"):
		path += "/chat/completions"
		openAI = true
	case strings.HasSuffix(path, "/api/generate"):
	default:
		path += "/api/generate"
	}
	parsed.Path = path
	parsed.RawPath = ""
	return parsed.String(), openAI, nil
}
