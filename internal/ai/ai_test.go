package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMockSuggestions(t *testing.T) {
	client := NewClient("", "unused-model")
	client.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("mock mode made a network request")
		return nil, errors.New("unexpected request")
	})
	if !client.MockMode() {
		t.Fatal("empty backend must select mock mode")
	}
	for _, input := range []string{"Happy Birth", "Hello", "Welcome", "Astana Hub", "Launch\nnow", strings.Repeat("a", 1000)} {
		t.Run(input[:min(len(input), 20)], func(t *testing.T) {
			first, err := client.Suggestions(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			second, err := client.Suggestions(context.Background(), input)
			if err != nil || !reflect.DeepEqual(first, second) {
				t.Fatalf("mock mode is not deterministic: %v, %v, %v", first, second, err)
			}
			if len(first) < 3 || len(first) > 5 {
				t.Fatalf("wrong suggestion count: %d", len(first))
			}
			seen := make(map[string]bool)
			for _, value := range first {
				if !validASCIIText(value) || seen[value] {
					t.Errorf("invalid or duplicate completion: %q", value)
				}
				seen[value] = true
				if input == "Happy Birth" && !strings.HasPrefix(value, "Happy Birthday") {
					t.Errorf("not a relevant birthday completion: %q", value)
				}
			}
		})
	}
}

func TestNewClientFromEnv(t *testing.T) {
	t.Setenv("LLM_BASE_URL", " https://example.test/v1/ ")
	t.Setenv("LLM_MODEL", " configured-model ")
	t.Setenv("LLM_API_KEY", " secret-token ")
	client := NewClientFromEnv()
	if client.MockMode() || client.model != "configured-model" || client.apiKey != "secret-token" {
		t.Fatal("configuration was not read from the environment")
	}
	if client.http.Timeout != 10*time.Second {
		t.Fatalf("HTTP timeout = %s, want 10s", client.http.Timeout)
	}
	t.Setenv("LLM_BASE_URL", "")
	if !NewClientFromEnv().MockMode() {
		t.Fatal("a model or API key alone must not enable live mode")
	}
}

func TestEndpoint(t *testing.T) {
	for _, test := range []struct {
		base   string
		want   string
		openAI bool
	}{
		{"http://localhost:11434", "http://localhost:11434/api/generate", false},
		{"http://localhost:11434/", "http://localhost:11434/api/generate", false},
		{"https://example.test/proxy/api/generate/", "https://example.test/proxy/api/generate", false},
		{"https://example.test/v1/", "https://example.test/v1/chat/completions", true},
		{"https://example.test/proxy/v1", "https://example.test/proxy/v1/chat/completions", true},
		{"https://example.test/chat/completions", "https://example.test/chat/completions", true},
	} {
		t.Run(test.base, func(t *testing.T) {
			got, openAI, err := NewClient(test.base, "model").endpoint()
			if err != nil || got != test.want || openAI != test.openAI {
				t.Fatalf("endpoint() = %q, %v, %v", got, openAI, err)
			}
		})
	}
	for _, invalid := range []string{"localhost:11434", "ftp://example.test", "http:///path", "http://user:password@example.test", "http://example.test?token=secret", "http://example.test/#fragment"} {
		if _, _, err := NewClient(invalid, "model").endpoint(); !errors.Is(err, ErrUnavailable) {
			t.Errorf("accepted invalid backend %q: %v", invalid, err)
		}
	}
}

func TestLiveSuggestionsProtocols(t *testing.T) {
	t.Setenv("LLM_API_KEY", "test-token")
	for _, openAI := range []bool{false, true} {
		t.Run(fmt.Sprintf("openai=%v", openAI), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				wantPath := "/api/generate"
				if openAI {
					wantPath = "/v1/chat/completions"
				}
				if r.Method != http.MethodPost || r.URL.Path != wantPath || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Authorization") != "Bearer test-token" {
					t.Errorf("unexpected request: %s %s, headers %v", r.Method, r.URL.Path, r.Header)
				}
				var request struct {
					Model    string `json:"model"`
					Prompt   string `json:"prompt"`
					Stream   bool   `json:"stream"`
					Messages []struct {
						Role    string `json:"role"`
						Content string `json:"content"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if request.Model != "configured-model" || request.Stream {
					t.Errorf("invalid request: %+v", request)
				}
				content := "1. Hello!\n2. Hello World\n3. Hello Team"
				if openAI {
					if len(request.Messages) != 1 || request.Messages[0].Role != "user" || !strings.Contains(request.Messages[0].Content, `Input: "Hello"`) {
						t.Errorf("unexpected messages: %+v", request.Messages)
					}
					writeOpenAI(w, content)
				} else {
					if !strings.Contains(request.Prompt, `Input: "Hello"`) {
						t.Errorf("unexpected prompt: %q", request.Prompt)
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"response": content, "done": true})
				}
			}))
			defer server.Close()
			base := server.URL
			if openAI {
				base += "/v1"
			}
			client := NewClient(base, "configured-model")
			first, err := client.Suggestions(context.Background(), "Hello")
			if err != nil || !reflect.DeepEqual(first, []string{"Hello!", "Hello World", "Hello Team"}) {
				t.Fatalf("unexpected live suggestions: %v, %v", first, err)
			}
			first[0] = "mutated by caller"
			cached, err := client.Suggestions(context.Background(), "Hello")
			if err != nil || cached[0] != "Hello!" || calls.Load() != 1 {
				t.Fatalf("cache did not isolate its result: %v, calls=%d, %v", cached, calls.Load(), err)
			}
		})
	}
}

func TestLiveErrorsAndRetry(t *testing.T) {
	for _, test := range []struct {
		name    string
		status  int
		body    string
		want    error
		wantTry int32
	}{
		{"unavailable", 503, "down", ErrUnavailable, 2},
		{"rate limited", 429, "slow down", ErrUnavailable, 2},
		{"authentication failure", 401, "bad key", ErrUnavailable, 1},
		{"invalid json", 200, "{", ErrInvalidResponse, 1},
		{"empty response", 200, `{"response":""}`, ErrInvalidResponse, 1},
		{"incomplete response", 200, `{"response":"Hello!\nHello World\nHello Team","done":false}`, ErrInvalidResponse, 1},
		{"backend error", 200, `{"error":"failed"}`, ErrInvalidResponse, 1},
		{"not enough completions", 200, `{"response":"One\nTwo"}`, ErrInvalidResponse, 1},
		{"oversized response", 200, strings.Repeat("x", maxResponseSize+1), ErrInvalidResponse, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			got, err := NewClient(server.URL, "model").Suggestions(context.Background(), "Hello")
			if !errors.Is(err, test.want) || got != nil || calls.Load() != test.wantTry {
				t.Fatalf("got %v, %v, calls=%d; want %v, calls=%d", got, err, calls.Load(), test.want, test.wantTry)
			}
		})
	}
	t.Run("transient recovery", func(t *testing.T) {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"response": "Hello!\nHello World\nHello Team"})
		}))
		defer server.Close()
		got, err := NewClient(server.URL, "model").Suggestions(context.Background(), "Hello")
		if err != nil || len(got) != 3 || calls.Load() != 2 {
			t.Fatalf("retry failed: %v, %v, calls=%d", got, err, calls.Load())
		}
	})
}

func TestNetworkFailureDoesNotLeakBackendConfiguration(t *testing.T) {
	client := NewClient("https://private-backend.test/secret-path", "model")
	var calls int
	client.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("failed to connect to private-backend.test with secret-token")
	})
	_, err := client.Suggestions(context.Background(), "Hello")
	if !errors.Is(err, ErrUnavailable) || calls != 2 {
		t.Fatalf("unexpected network failure: %v, calls=%d", err, calls)
	}
	if strings.Contains(err.Error(), "private-backend") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("network error exposes private configuration: %v", err)
	}
}

func TestClientDeadlinesAndCancellation(t *testing.T) {
	client := NewClient("http://example.test", "model")
	var deadlines []time.Time
	client.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > requestTimeout {
			t.Error("request is not bounded by the total 10-second deadline")
		}
		deadlines = append(deadlines, deadline)
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})
	if _, err := client.Suggestions(context.Background(), "Hello"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(deadlines) != 2 || !deadlines[0].Equal(deadlines[1]) {
		t.Fatalf("retry reset the total deadline: %v", deadlines)
	}

	client.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := client.Suggestions(ctx, "Hello"); !errors.Is(err, ErrUnavailable) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unexpected timeout error: %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("client ignored the earlier caller deadline")
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	for _, method := range []func(context.Context) error{
		func(ctx context.Context) error { _, err := NewClient("", "").Suggestions(ctx, "Hello"); return err },
		func(ctx context.Context) error { _, err := NewClient("", "").Variations(ctx, "Hello"); return err },
	} {
		if err := method(canceled); !errors.Is(err, context.Canceled) {
			t.Errorf("canceled call returned %v", err)
		}
	}
}

func TestConfigurationFailureAndRedirect(t *testing.T) {
	if _, err := NewClient("https://example.test", "").Suggestions(context.Background(), "Hello"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing model did not fail: %v", err)
	}
	var destinationCalls atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		destinationCalls.Add(1)
	}))
	defer destination.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	if _, err := NewClient(redirect.URL, "model").Suggestions(context.Background(), "Hello"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("redirect returned %v", err)
	}
	if destinationCalls.Load() != 0 {
		t.Fatal("client sent the prompt to a redirect destination")
	}
}

func TestParseSuggestions(t *testing.T) {
	got, err := parseSuggestions("1. Hello!\r\n- Hello World\n* Hello Team\nHello Team\nПривет\nBad\tText\n" + strings.Repeat("x", 50))
	if err != nil || !reflect.DeepEqual(got, []string{"Hello!", "Hello World", "Hello Team"}) {
		t.Fatalf("unexpected parsed suggestions: %v, %v", got, err)
	}
	for _, invalid := range []string{"", "Hello\nHello\nHello", "One\nTwo\nПривет", "One\nTwo\nInvalid\ttext", "One\nTwo\n" + strings.Repeat("x", 50)} {
		if _, err := parseSuggestions(invalid); !errors.Is(err, ErrInvalidResponse) {
			t.Errorf("accepted invalid suggestions %q", invalid)
		}
	}
}

func TestMockVariations(t *testing.T) {
	for _, input := range []string{"hello", "WELCOME", "Launch\nnow", "!!!", strings.Repeat("a", 1000)} {
		variations, err := NewClient("", "").Variations(context.Background(), input)
		if err != nil || len(variations) != 4 {
			t.Fatalf("unexpected mock variations: %v, %v", variations, err)
		}
		content, err := json.Marshal(variations)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := parseVariations(string(content)); err != nil {
			t.Errorf("mock output violates the live response contract: %v", err)
		}
	}
}

func TestLiveVariationsAndValidation(t *testing.T) {
	variations := mockVariations("hello")
	content, _ := json.Marshal(variations)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeOpenAI(w, string(content))
	}))
	defer server.Close()
	got, err := NewClient(server.URL+"/v1", "model").Variations(context.Background(), "hello")
	if err != nil || !reflect.DeepEqual(got, variations) {
		t.Fatalf("unexpected live variations: %v, %v", got, err)
	}
	for _, wrap := range []func(string) string{
		func(s string) string { return s },
		func(s string) string { return "```json\n" + s + "\n```" },
	} {
		if _, err := parseVariations(wrap(string(content))); err != nil {
			t.Errorf("valid response rejected: %v", err)
		}
	}
	for name, change := range map[string]func([]Variation) []Variation{
		"too few":          func(v []Variation) []Variation { return v[:2] },
		"too many":         func(v []Variation) []Variation { return append(v, v[0], v[1]) },
		"invalid banner":   func(v []Variation) []Variation { v[0].SuggestedBanner = "unknown"; return v },
		"non ASCII":        func(v []Variation) []Variation { v[0].Text = "Привет"; return v },
		"long text":        func(v []Variation) []Variation { v[0].Text = strings.Repeat("x", 50); return v },
		"long description": func(v []Variation) []Variation { v[0].Description = strings.Repeat("x", 30); return v },
		"no description":   func(v []Variation) []Variation { v[0].Description = " "; return v },
		"control chars":    func(v []Variation) []Variation { v[0].Description = "bad\ntext"; return v },
		"duplicate text":   func(v []Variation) []Variation { v[0].Text = v[1].Text; return v },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := change(append([]Variation(nil), variations...))
			body, _ := json.Marshal(invalid)
			if _, err := parseVariations(string(body)); !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("accepted invalid variations: %s", body)
			}
		})
	}
	if _, err := parseVariations(string(content) + " trailing prose"); !errors.Is(err, ErrInvalidResponse) {
		t.Fatal("accepted trailing garbage")
	}
}

func TestRecommendationRules(t *testing.T) {
	for _, test := range []struct {
		text string
		want string
	}{
		{"WELCOME", "shadow"},
		{"Hi", "shadow"},
		{"123456", "shadow"},
		{"THIS IS A LONG TITLE", "shadow"},
		{"This is a long message", "standard"},
		{"HelloWorld", "thinkertoy"},
		{"*** wow ***", "thinkertoy"},
		{"!@#$%^", "thinkertoy"},
		{"Hello\n\n\n\n\n\n\nworld", "standard"},
	} {
		t.Run(test.text, func(t *testing.T) {
			got := RecommendBanner(test.text)
			if got.Recommended != test.want || got.Reasoning == "" || len(got.Alternatives) != 2 {
				t.Fatalf("unexpected recommendation: %+v", got)
			}
			if !reflect.DeepEqual(got, RecommendBanner(test.text)) {
				t.Fatal("recommendations are not deterministic")
			}
			seen := map[string]bool{got.Recommended: true}
			for _, alternative := range got.Alternatives {
				if !validBanner(alternative.Banner) || seen[alternative.Banner] || alternative.Score < 0 || alternative.Score > 1 || alternative.Reason == "" {
					t.Errorf("invalid alternative: %+v", alternative)
				}
				seen[alternative.Banner] = true
			}
		})
	}
}

func TestSuggestionCacheExpirationCapacityAndConcurrency(t *testing.T) {
	var cache suggestionCache
	now := time.Now()
	original := []string{"One", "Two", "Three"}
	cache.put("hello", original, now)
	original[0] = "changed"
	if got, ok := cache.get("hello", now.Add(cacheTTL-time.Nanosecond)); !ok || got[0] != "One" {
		t.Fatalf("cache expired early or shared caller memory: %v, %v", got, ok)
	}
	if _, ok := cache.get("hello", now.Add(cacheTTL)); ok {
		t.Fatal("entry survived expiration")
	}
	for i := 0; i < cacheCapacity+1; i++ {
		cache.put(fmt.Sprint(i), original, now.Add(time.Duration(i)*time.Millisecond))
	}
	if len(cache.entries) != cacheCapacity {
		t.Fatalf("cache size %d exceeds capacity %d", len(cache.entries), cacheCapacity)
	}
	if _, ok := cache.get("0", now); ok {
		t.Fatal("oldest entry was not evicted")
	}
	var group sync.WaitGroup
	for i := 0; i < 32; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			for j := 0; j < 50; j++ {
				key := fmt.Sprintf("%d-%d", i, j)
				cache.put(key, []string{"One", "Two", "Three"}, now)
				if got, ok := cache.get(key, now); ok {
					got[0] = "changed"
				}
			}
		}(i)
	}
	group.Wait()
	if len(cache.entries) > cacheCapacity {
		t.Fatal("concurrent writes exceeded cache capacity")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func writeOpenAI(w http.ResponseWriter, content string) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"choices": []map[string]any{{"message": map[string]string{"content": content}}},
	})
}
