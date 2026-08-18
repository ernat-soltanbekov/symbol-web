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

type Client struct {
	baseURL string
	model   string
	apiKey  string
	http    *http.Client
}

// NewClient создаёт LLM-клиент; пустой адрес включает mock-режим.
func NewClient(baseURL, model string) *Client {
	return &Client{
		baseURL: strings.TrimSpace(baseURL),
		model:   strings.TrimSpace(model),
		apiKey:  strings.TrimSpace(os.Getenv("LLM_API_KEY")),
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

// NewClientFromEnv читает настройки LLM из переменных окружения.
func NewClientFromEnv() *Client {
	return NewClient(os.Getenv("LLM_BASE_URL"), os.Getenv("LLM_MODEL"))
}

// MockMode сообщает, работает ли клиент без внешнего LLM-сервиса.
func (c *Client) MockMode() bool {
	return c.baseURL == ""
}

// Suggestions возвращает от трёх до пяти коротких продолжений текста.
func (c *Client) Suggestions(ctx context.Context, text string) ([]string, error) {
	if c.MockMode() {
		return mockSuggestions(text), nil
	}
	content, err := c.complete(ctx, suggestionPrompt(text))
	if err != nil {
		return nil, err
	}
	return parseSuggestions(content)
}

// Variations возвращает творческие варианты текста и подходящие баннеры.
func (c *Client) Variations(ctx context.Context, text string) ([]Variation, error) {
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
	if c.model == "" {
		return "", fmt.Errorf("%w: переменная LLM_MODEL не задана", ErrUnavailable)
	}

	endpoint, openAI, err := c.endpoint()
	if err != nil {
		return "", err
	}

	var body []byte
	if openAI {
		body, err = json.Marshal(map[string]any{
			"model": c.model,
			"messages": []map[string]string{
				{"role": "user", "content": prompt},
			},
			"temperature": 0.7,
		})
	} else {
		body, err = json.Marshal(map[string]any{
			"model":  c.model,
			"prompt": prompt,
			"stream": false,
		})
	}
	if err != nil {
		return "", fmt.Errorf("формирование LLM-запроса: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	request.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	response, err := c.http.Do(request)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer response.Body.Close()

	limited := io.LimitReader(response.Body, 2<<20)
	responseBody, err := io.ReadAll(limited)
	if err != nil {
		return "", fmt.Errorf("%w: чтение ответа: %v", ErrUnavailable, err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("%w: HTTP %d", ErrUnavailable, response.StatusCode)
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
			return "", fmt.Errorf("%w: OpenAI-ответ не содержит текста", ErrInvalidResponse)
		}
		return payload.Choices[0].Message.Content, nil
	}

	var payload struct {
		Response string `json:"response"`
	}
	if err := json.Unmarshal(responseBody, &payload); err != nil || strings.TrimSpace(payload.Response) == "" {
		return "", fmt.Errorf("%w: Ollama-ответ не содержит текста", ErrInvalidResponse)
	}
	return payload.Response, nil
}

func (c *Client) endpoint() (string, bool, error) {
	parsed, err := url.Parse(c.baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", false, fmt.Errorf("%w: неверный LLM_BASE_URL", ErrUnavailable)
	}

	base := strings.TrimRight(c.baseURL, "/")
	path := strings.TrimRight(parsed.Path, "/")
	switch {
	case strings.HasSuffix(path, "/chat/completions"):
		return base, true, nil
	case strings.HasSuffix(path, "/v1"):
		return base + "/chat/completions", true, nil
	case strings.HasSuffix(path, "/api/generate"):
		return base, false, nil
	default:
		return base + "/api/generate", false, nil
	}
}
