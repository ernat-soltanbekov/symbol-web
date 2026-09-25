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

func TestMockVariationsRemainDistinctAfterTruncation(t *testing.T) {
	for _, input := range []string{
		strings.Repeat("!", 49),
		strings.Repeat("1", 48) + "!",
		strings.Repeat("~", 1000),
		strings.Repeat(".", 46) + " :)",
	} {
		t.Run(input[:12], func(t *testing.T) {
			variations, err := NewClient("", "").Variations(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			assertVariationsContract(t, variations)
		})
	}
}

func TestProviderRejectsInvalidUTF8(t *testing.T) {
	// encoding/json replaces malformed UTF-8 unless it is rejected before decoding.
	// The corruption is in a description, where otherwise printable Unicode is valid.
	content := `[{"text":"One","description":"bad` + string([]byte{0xff}) + `","suggested_banner":"standard"},` +
		`{"text":"Two","description":"Bold","suggested_banner":"shadow"},` +
		`{"text":"Three","description":"Friendly","suggested_banner":"thinkertoy"}]`
	if _, err := parseVariations(content); !errors.Is(err, ErrInvalidResponse) {
		t.Errorf("accepted malformed UTF-8 variation JSON: %v", err)
	}
	for _, openAI := range []bool{false, true} {
		t.Run(fmt.Sprintf("openai=%v", openAI), func(t *testing.T) {
			quoted, err := json.Marshal(strings.ReplaceAll(content, "\xff", "CORRUPT_UTF8"))
			if err != nil {
				t.Fatal(err)
			}
			// Inject an invalid byte into the provider's outer JSON string.
			quoted = []byte(strings.ReplaceAll(string(quoted), "CORRUPT_UTF8", "\xff"))
			body := `{"response":` + string(quoted) + `,"done":true}`
			if openAI {
				body = `{"choices":[{"message":{"content":` + string(quoted) + `}}]}`
			}
			client := NewClient("http://provider.test", "model")
			if openAI {
				client.baseURL += "/v1"
			}
			client.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})
			if _, err := client.Variations(context.Background(), "Hello"); !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("accepted malformed UTF-8 provider response: %v", err)
			}
		})
	}
}

func TestSuggestionMarkdownFencesDoNotCountAsCompletions(t *testing.T) {
	for _, fence := range []string{"```", "```text", "```plaintext"} {
		t.Run(fence, func(t *testing.T) {
			if got, err := parseSuggestions(fence + "\nOne\nTwo\n```"); !errors.Is(err, ErrInvalidResponse) {
				t.Errorf("fences counted as completions: %v, %v", got, err)
			}
			got, err := parseSuggestions(fence + "\r\nOne\r\nTwo\r\nThree\r\n```")
			if err != nil || !reflect.DeepEqual(got, []string{"One", "Two", "Three"}) {
				t.Errorf("valid fenced completions: %v, %v", got, err)
			}
		})
	}
}

func TestLiveResponseBodyFailures(t *testing.T) {
	for _, failure := range []string{"truncated", "disconnected", "delayed"} {
		t.Run(failure, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				switch failure {
				case "truncated":
					w.Header().Set("Content-Length", "999")
					_, _ = io.WriteString(w, `{"response":"unfinished`)
				case "disconnected":
					connection, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = connection.Close()
				case "delayed":
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
					<-r.Context().Done()
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if failure == "delayed" {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, 75*time.Millisecond)
				defer stop()
			}
			started := time.Now()
			got, err := NewClient(server.URL, "model").Suggestions(ctx, "Hello")
			if got != nil || !errors.Is(err, ErrUnavailable) {
				t.Fatalf("body failure returned %v, %v", got, err)
			}
			wantCalls := int32(2)
			if failure == "delayed" {
				wantCalls = 1
				if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > time.Second {
					t.Fatalf("body read did not respect caller deadline: %v, %s", err, time.Since(started))
				}
			}
			if calls.Load() != wantCalls {
				t.Fatalf("calls=%d, want %d", calls.Load(), wantCalls)
			}
		})
	}
}

func TestCancellationDuringRetryWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls int
	client := NewClient("http://provider.test", "model")
	client.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		cancel()
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})
	_, err := client.Suggestions(ctx, "Hello")
	if calls != 1 || !errors.Is(err, context.Canceled) || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("canceled retry returned %v, calls=%d", err, calls)
	}
}

func TestProviderStatusClassification(t *testing.T) {
	for _, status := range []int{301, 400, 401, 403, 404, 408, 429, 500, 502, 503, 504} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			client := NewClient("http://provider.test", "model")
			var calls int
			client.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("private upstream error")), Header: make(http.Header)}, nil
			})
			_, err := client.Suggestions(context.Background(), "Hello")
			wantCalls := 1
			if status == 429 || status == 500 || status == 502 || status == 503 || status == 504 {
				wantCalls = 2
			}
			if !errors.Is(err, ErrUnavailable) || calls != wantCalls {
				t.Fatalf("status %d: error=%v, calls=%d, want %d", status, err, calls, wantCalls)
			}
			if strings.Contains(err.Error(), "private") {
				t.Fatal("error leaked upstream response body")
			}
		})
	}
}

func TestLiveSuggestionsConcurrentCacheIsolation(t *testing.T) {
	client := NewClient("http://provider.test", "model")
	client.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"response":"One\nTwo\nThree","done":true}`)), Header: make(http.Header)}, nil
	})
	var workers sync.WaitGroup
	for i := 0; i < 32; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 20; j++ {
				values, err := client.Suggestions(context.Background(), "Hello")
				if err != nil || !reflect.DeepEqual(values, []string{"One", "Two", "Three"}) {
					t.Errorf("cache returned %v, %v", values, err)
					return
				}
				values[0] = "caller mutation"
			}
		}()
	}
	workers.Wait()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Suggestions(ctx, "Hello"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cache bypassed caller cancellation: %v", err)
	}
}

func assertSuggestionsContract(t *testing.T, values []string) {
	t.Helper()
	if len(values) < 3 || len(values) > 5 {
		t.Fatalf("suggestion count %d, want 3-5", len(values))
	}
	seen := make(map[string]bool)
	for _, value := range values {
		if !validASCIIText(value) || seen[value] {
			t.Fatalf("invalid or duplicate suggestion %q", value)
		}
		seen[value] = true
	}
}

func assertVariationsContract(t *testing.T, values []Variation) {
	t.Helper()
	if len(values) < 3 || len(values) > 5 {
		t.Fatalf("variation count %d, want 3-5", len(values))
	}
	seen := make(map[string]bool)
	for _, value := range values {
		if !validASCIIText(value.Text) || seen[value.Text] || !validDescription(value.Description) || !validBanner(value.SuggestedBanner) {
			t.Fatalf("invalid or duplicate variation %+v in %+v", value, values)
		}
		seen[value.Text] = true
	}
}

func FuzzMockResponseContracts(f *testing.F) {
	for _, input := range []string{"Hello", "Happy Birth", "!!!", "~ hello ~", "\r\nHello\r\nworld\r\n", strings.Repeat("!", 49), strings.Repeat("a", 1000)} {
		f.Add(input)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) == 0 || len(input) > 1000 || strings.TrimSpace(input) == "" {
			t.Skip()
		}
		for _, char := range input {
			if char != '\r' && char != '\n' && (char < ' ' || char > '~') {
				t.Skip()
			}
		}
		assertSuggestionsContract(t, mockSuggestions(input))
		assertVariationsContract(t, mockVariations(input))
	})
}

func FuzzParseSuggestions(f *testing.F) {
	for _, input := range []string{"One\nTwo\nThree", "1. Hello!\r\n- Hello World\r\n* Hello Team", "```text\nOne\nTwo\n```", "```\nOne\nTwo\nThree\n```", "", "One\nTwo\nBad\x00Text", "One\nTwo\nПривет", strings.Repeat("x", 50)} {
		f.Add(input)
	}
	f.Fuzz(func(t *testing.T, input string) {
		values, err := parseSuggestions(input)
		if err == nil {
			assertSuggestionsContract(t, values)
		} else if !errors.Is(err, ErrInvalidResponse) || values != nil {
			t.Fatalf("invalid parse result: %v, %v", values, err)
		}
	})
}

func FuzzParseVariations(f *testing.F) {
	valid, _ := json.Marshal(mockVariations("hello"))
	for _, input := range []string{string(valid), "```json\n" + string(valid) + "\n```", "[]", "null", "[{}]", "[", "\xff"} {
		f.Add(input)
	}
	f.Fuzz(func(t *testing.T, input string) {
		values, err := parseVariations(input)
		if err == nil {
			assertVariationsContract(t, values)
		} else if !errors.Is(err, ErrInvalidResponse) || values != nil {
			t.Fatalf("invalid parse result: %v, %v", values, err)
		}
	})
}
