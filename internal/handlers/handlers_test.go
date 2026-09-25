package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"symbol-web/internal/ai"
	"symbol-web/internal/ascii"
)

func testHandler() (*Handler, *http.ServeMux) {
	handler := New(ascii.NewGenerator("../.."), ai.NewClient("", ""), "../../templates", log.New(io.Discard, "", 0))
	mux := http.NewServeMux()
	handler.Register(mux)
	return handler, mux
}

func perform(handler http.Handler, method, path, body, contentType, accept string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if accept != "" {
		request.Header.Set("Accept", accept)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestRoutesAndMethods(t *testing.T) {
	_, mux := testHandler()
	for _, tc := range []struct {
		method, path string
		status       int
		allow        string
	}{
		{"GET", "/", 200, ""}, {"POST", "/", 405, "GET"},
		{"GET", "/missing", 404, ""}, {"GET", "/api/missing", 404, ""},
		{"GET", "/symbol-art", 405, "POST"}, {"GET", "/api/suggest", 405, "POST"},
		{"GET", "/api/variations", 405, "POST"}, {"GET", "/api/recommend-banner", 405, "POST"},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			response := perform(mux, tc.method, tc.path, "", "", "")
			if response.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", response.Code, tc.status, response.Body.String())
			}
			if response.Header().Get("Allow") != tc.allow {
				t.Errorf("Allow = %q", response.Header().Get("Allow"))
			}
			if strings.HasPrefix(tc.path, "/api/") && !json.Valid(response.Body.Bytes()) {
				t.Fatal("API error must be JSON")
			}
		})
	}
}

func TestSymbolArtHTMLAndJSON(t *testing.T) {
	_, mux := testHandler()
	for _, banner := range []string{"standard", "shadow", "thinkertoy"} {
		for _, accept := range []string{"text/html", "application/json"} {
			t.Run(banner+accept, func(t *testing.T) {
				input := "{123}\n<Hello> (World)!"
				body := url.Values{"text": {input}, "banner": {banner}}.Encode()
				response := perform(mux, "POST", "/symbol-art", body, "application/x-www-form-urlencoded", accept)
				if response.Code != 200 {
					t.Fatalf("status %d: %s", response.Code, response.Body.String())
				}
				if accept == "application/json" {
					var data PageData
					if err := json.Unmarshal(response.Body.Bytes(), &data); err != nil {
						t.Fatal(err)
					}
					expected, err := ascii.NewGenerator("../..").Generate(input, banner)
					if err != nil {
						t.Fatal(err)
					}
					if data.Result != expected || data.Text != input || data.Banner != banner {
						t.Fatal("JSON lost text, banner or result")
					}
				} else if !strings.Contains(response.Body.String(), "ascii-result") || !strings.Contains(response.Body.String(), "&lt;Hello&gt;") {
					t.Fatal("HTML result missing or input not escaped")
				}
			})
		}
	}
}

func TestSymbolArtRejectsBadForms(t *testing.T) {
	_, mux := testHandler()
	for _, body := range []string{
		"text=&banner=standard", "text=Hello&banner=other", "text=Hello&banner=../../secret",
		url.Values{"text": {"Привет"}, "banner": {"standard"}}.Encode(),
		"text=" + strings.Repeat("a", 1001) + "&banner=standard", "text=abc%00&banner=standard",
		"text=abc&banner=standard&banner=shadow", "text=abc&text=xyz&banner=standard",
		"text=hello", "banner=standard", "text=%zz&banner=standard", strings.Repeat("a", 65<<10),
	} {
		response := perform(mux, "POST", "/symbol-art?text=query&banner=standard", body, "application/x-www-form-urlencoded", "application/json")
		if response.Code != 400 || !json.Valid(response.Body.Bytes()) {
			t.Errorf("bad form: status %d, response %.100s", response.Code, response.Body.String())
		}
	}
	response := perform(mux, "POST", "/symbol-art", `{"text":"hello"}`, "application/json", "application/json")
	if response.Code != 400 {
		t.Errorf("JSON form status %d", response.Code)
	}
}

func TestJSONEndpoints(t *testing.T) {
	_, mux := testHandler()
	for _, path := range []string{"/api/suggest", "/api/recommend-banner", "/api/variations"} {
		t.Run(path, func(t *testing.T) {
			first := perform(mux, "POST", path, `{"text":"WELCOME"}`, "application/json", "")
			second := perform(mux, "POST", path, `{"text":"WELCOME"}`, "application/json", "")
			if first.Code != 200 || !json.Valid(first.Body.Bytes()) {
				t.Fatalf("%d: %s", first.Code, first.Body.String())
			}
			if first.Body.String() != second.Body.String() {
				t.Fatal("mock/rules response is not deterministic")
			}
			if !strings.HasPrefix(first.Header().Get("Content-Type"), "application/json") {
				t.Fatal("wrong content type")
			}
		})
	}
	response := perform(mux, "POST", "/api/suggest", `{"text":"Happy Birth"}`, "application/json", "")
	var suggestions struct {
		Suggestions []string `json:"suggestions"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &suggestions); err != nil {
		t.Fatal(err)
	}
	if len(suggestions.Suggestions) < 3 || len(suggestions.Suggestions) > 5 {
		t.Fatal("need 3-5 suggestions")
	}
	for _, text := range suggestions.Suggestions {
		if len(text) >= 50 || ascii.ValidateText(text, false) != nil {
			t.Fatalf("unrenderable suggestion %q", text)
		}
	}
	var recommendation ai.Recommendation
	response = perform(mux, "POST", "/api/recommend-banner", `{"text":"WELCOME"}`, "application/json", "")
	if err := json.Unmarshal(response.Body.Bytes(), &recommendation); err != nil {
		t.Fatal(err)
	}
	if recommendation.Recommended != "shadow" || recommendation.Reasoning == "" || len(recommendation.Alternatives) != 2 {
		t.Fatalf("invalid recommendation: %+v", recommendation)
	}
	var variations struct {
		Variations []ai.Variation `json:"variations"`
	}
	response = perform(mux, "POST", "/api/variations", `{"text":"hello"}`, "application/json", "")
	if err := json.Unmarshal(response.Body.Bytes(), &variations); err != nil {
		t.Fatal(err)
	}
	if len(variations.Variations) < 3 || len(variations.Variations) > 5 {
		t.Fatal("need 3-5 variations")
	}
}

func TestJSONValidation(t *testing.T) {
	_, mux := testHandler()
	for _, path := range []string{"/api/suggest", "/api/recommend-banner", "/api/variations"} {
		for _, body := range []string{"", "null", "[]", "{}", `{"text":12}`, `{"text":""}`, `{"text":"   "}`, `{"text":"Привет"}`, `{"text":"hi\tthere"}`, `{"text":"Hello","extra":true}`, `{"text":"Hello"}{}`, `{"text":"Hello"} junk`, `{"text":"` + strings.Repeat("x", 1001) + `"}`, strings.Repeat(" ", 65<<10)} {
			response := perform(mux, "POST", path, body, "application/json", "")
			if response.Code != 400 || !json.Valid(response.Body.Bytes()) {
				t.Errorf("%s body %.80q: %d %s", path, body, response.Code, response.Body.String())
			}
		}
		response := perform(mux, "POST", path, `{"text":"Hello"}`, "text/plain", "")
		if response.Code != 400 {
			t.Errorf("invalid content type accepted: %s", path)
		}
	}
	if response := perform(mux, "POST", "/api/suggest", `{"text":"Hi"}`, "application/json", ""); response.Code != 400 {
		t.Errorf("short suggestion status %d", response.Code)
	}
}

func TestTemplateFailures(t *testing.T) {
	for _, tc := range []struct {
		name, index, result string
		status              int
	}{
		{"missing", "", "", 404},
		{"missing partial", "page", "", 404},
		{"syntax", "{{if}}", "result", 500},
		{"execution", "prefix{{.MissingField}}", "result", 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range map[string]string{"index.html": tc.index, "result.html": tc.result} {
				if content != "" {
					if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			handler := New(ascii.NewGenerator("../.."), ai.NewClient("", ""), dir, nil)
			response := perform(http.HandlerFunc(handler.Home), "GET", "/", "", "", "")
			if response.Code != tc.status {
				t.Fatalf("%d: %s", response.Code, response.Body.String())
			}
			if strings.HasPrefix(response.Body.String(), "prefix") {
				t.Fatal("partial template leaked before error")
			}
		})
	}
}

func TestBannerFileFailures(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		dir := t.TempDir()
		expected := 404
		if malformed {
			expected = 500
			if err := os.WriteFile(filepath.Join(dir, "standard.txt"), []byte("broken font"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		handler := New(ascii.NewGenerator(dir), ai.NewClient("", ""), "../../templates", log.New(io.Discard, "", 0))
		for _, accept := range []string{"text/html", "application/json"} {
			response := perform(http.HandlerFunc(handler.SymbolArt), "POST", "/symbol-art", "text=Hello&banner=standard", "application/x-www-form-urlencoded", accept)
			if response.Code != expected {
				t.Errorf("status %d, want %d", response.Code, expected)
			}
		}
	}
}

func TestLiveFailuresAndRuleIsolation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   int
	}{
		{"unavailable", 503, "offline", 503}, {"invalid JSON", 200, "invalid", 500},
		{"invalid completions", 200, `{"response":"only one"}`, 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			handler := New(ascii.NewGenerator("../.."), ai.NewClient(server.URL, "test-model"), "../../templates", log.New(io.Discard, "", 0))
			mux := http.NewServeMux()
			handler.Register(mux)
			for _, path := range []string{"/api/suggest", "/api/variations"} {
				response := perform(mux, "POST", path, `{"text":"Hello"}`, "application/json", "")
				if response.Code != tc.want || !json.Valid(response.Body.Bytes()) {
					t.Errorf("%s: %d %s", path, response.Code, response.Body.String())
				}
			}
			if response := perform(mux, "POST", "/api/recommend-banner", `{"text":"WELCOME"}`, "application/json", ""); response.Code != 200 {
				t.Fatal("rules depend on LLM")
			}
			if response := perform(mux, "GET", "/", "", "", ""); response.Code != 200 {
				t.Fatal("server stopped after LLM failure")
			}
		})
	}
}

func TestConcurrentRequests(t *testing.T) {
	_, mux := testHandler()
	var group sync.WaitGroup
	for i := 0; i < 24; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			path, body, contentType := "/api/suggest", fmt.Sprintf(`{"text":"Hello %d"}`, i), "application/json"
			if i%2 == 0 {
				path, body, contentType = "/symbol-art", "text=Hello&banner=standard", "application/x-www-form-urlencoded"
			}
			if response := perform(mux, "POST", path, body, contentType, "application/json"); response.Code != 200 {
				t.Errorf("concurrent %s: %d", path, response.Code)
			}
		}(i)
	}
	group.Wait()
}

func TestLLMTimeoutIntegration(t *testing.T) {
	t.Parallel()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer backend.Close()
	handler := New(ascii.NewGenerator("../.."), ai.NewClient(backend.URL, "test-model"), "../../templates", log.New(io.Discard, "", 0))
	mux := http.NewServeMux()
	handler.Register(mux)
	started := time.Now()
	response := perform(mux, "POST", "/api/suggest", `{"text":"Hello"}`, "application/json", "")
	if response.Code != http.StatusServiceUnavailable || !json.Valid(response.Body.Bytes()) {
		t.Fatalf("timeout response: %d %s", response.Code, response.Body.String())
	}
	if elapsed := time.Since(started); elapsed < 9*time.Second || elapsed > 13*time.Second {
		t.Fatalf("expected 10-second timeout, took %s", elapsed)
	}
	if response := perform(mux, "GET", "/", "", "", ""); response.Code != http.StatusOK {
		t.Fatal("server stopped serving pages after a timeout")
	}
	if response := perform(mux, "POST", "/api/recommend-banner", `{"text":"WELCOME"}`, "application/json", ""); response.Code != http.StatusOK {
		t.Fatal("LLM timeout broke independent recommendations")
	}
}
