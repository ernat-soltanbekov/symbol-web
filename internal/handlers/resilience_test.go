package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAcceptJSONQuality(t *testing.T) {
	for _, tc := range []struct {
		accept string
		want   bool
	}{
		{"", false}, {"text/html", false}, {"application/json", true},
		{"application/json; q=0", false}, {"application/json; q=0.000", false},
		{"application/json;q=0.7", true}, {"application/json;q=1.0", true},
		{"application/json;q=invalid", false}, {"application/json;q=-1", false},
		{"application/json;q=2", false}, {"text/html, application/json;q=0.5", true},
	} {
		request := httptest.NewRequest(http.MethodPost, "/symbol-art", nil)
		request.Header.Set("Accept", tc.accept)
		if got := acceptsJSON(request); got != tc.want {
			t.Errorf("Accept %q: got %v want %v", tc.accept, got, tc.want)
		}
	}
}

func TestHTMLInjectionEscaped(t *testing.T) {
	_, mux := testHandler()
	input := `</textarea><script>alert("xss")</script><img src=x onerror=alert(1)>`
	response := perform(mux, "POST", "/symbol-art", url.Values{"text": {input}, "banner": {"standard"}}.Encode(), "application/x-www-form-urlencoded", "text/html")
	if response.Code != 200 {
		t.Fatalf("%d: %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), input) || !strings.Contains(response.Body.String(), "&lt;/textarea&gt;") {
		t.Fatal("HTML input escaped incorrectly")
	}
}

func TestConcurrentHTTPStress(t *testing.T) {
	if testing.Short() {
		t.Skip("bounded concurrent HTTP stress")
	}
	_, mux := testHandler()
	server := httptest.NewServer(mux)
	defer server.Close()
	client := server.Client()
	client.Timeout = 5 * time.Second
	const total = 1000
	const workers = 32
	var next atomic.Int64
	var failed atomic.Int64
	var group sync.WaitGroup
	started := time.Now()
	for worker := 0; worker < workers; worker++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= total {
					return
				}
				var path, body, contentType string
				var want int
				switch i % 5 {
				case 0:
					path, body, contentType, want = "/symbol-art", url.Values{"text": {strings.Repeat("W", 1000)}, "banner": {"shadow"}}.Encode(), "application/x-www-form-urlencoded", 200
				case 1:
					path, body, contentType, want = "/api/suggest", `{"text":"Happy Birth"}`, "application/json", 200
				case 2:
					path, body, contentType, want = "/api/recommend-banner", `{"text":"WELCOME"}`, "application/json", 200
				case 3:
					path, body, contentType, want = "/api/variations", `{"text":"Hello"}`, "application/json", 200
				default:
					path, body, contentType, want = "/api/suggest", `{"text":42}`, "application/json", 400
				}
				request, err := http.NewRequest(http.MethodPost, server.URL+path, strings.NewReader(body))
				if err != nil {
					t.Error(err)
					failed.Add(1)
					continue
				}
				request.Header.Set("Content-Type", contentType)
				request.Header.Set("Accept", "application/json")
				response, err := client.Do(request)
				if err != nil {
					t.Errorf("request %d: %v", i, err)
					failed.Add(1)
					continue
				}
				var data map[string]any
				decodeErr := json.NewDecoder(response.Body).Decode(&data)
				response.Body.Close()
				if response.StatusCode != want || decodeErr != nil {
					t.Errorf("request %d: status %d want %d; JSON %v", i, response.StatusCode, want, decodeErr)
					failed.Add(1)
				}
			}
		}()
	}
	group.Wait()
	t.Logf("%d HTTP requests, %d workers, %d failures, elapsed %s", total, workers, failed.Load(), time.Since(started).Round(time.Millisecond))
	if response := perform(mux, "GET", "/", "", "", ""); response.Code != 200 {
		t.Fatal("server unhealthy after stress")
	}
}

func FuzzJSONEndpoints(f *testing.F) {
	_, mux := testHandler()
	for _, seed := range []string{`{"text":"Hello"}`, `null`, `{"text":"<script>"}`, `{"text":"Hi"}`, `{"text":"Hello"} {}`, "\xff", "", `{"text":"\u0000"}`, `{"text":"Привет"}`} {
		f.Add(seed, uint8(0))
	}
	f.Fuzz(func(t *testing.T, body string, endpoint uint8) {
		if len(body) > 70<<10 {
			t.Skip()
		}
		paths := []string{"/api/suggest", "/api/recommend-banner", "/api/variations"}
		response := perform(mux, "POST", paths[int(endpoint)%len(paths)], body, "application/json", "")
		if response.Code != 200 && response.Code != 400 {
			t.Fatalf("unexpected status %d: %.100s", response.Code, response.Body.String())
		}
		if !json.Valid(response.Body.Bytes()) {
			t.Fatal("response is not valid JSON")
		}
		if response.Code == 200 {
			var result map[string]json.RawMessage
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			key := []string{"suggestions", "recommended", "variations"}[int(endpoint)%len(paths)]
			if _, ok := result[key]; !ok {
				t.Fatal(fmt.Sprintf("missing %s", key))
			}
		}
	})
}

func FuzzFormRequests(f *testing.F) {
	_, mux := testHandler()
	for _, body := range []string{"text=Hello&banner=standard", "text=%zz&banner=shadow", "text=%00&banner=thinkertoy", "text=hi&banner=../../etc/passwd", "banner=standard", "text=Hello&banner=standard&banner=shadow"} {
		f.Add(body)
	}
	f.Fuzz(func(t *testing.T, body string) {
		if len(body) > 70<<10 {
			t.Skip()
		}
		response := perform(mux, "POST", "/symbol-art", body, "application/x-www-form-urlencoded", "application/json")
		if response.Code != 200 && response.Code != 400 {
			t.Fatalf("status %d for %.100q", response.Code, body)
		}
		if !json.Valid(response.Body.Bytes()) {
			t.Fatal("non-JSON form response")
		}
	})
}
