package handlers

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"symbol-web/internal/ai"
	"symbol-web/internal/ascii"
)

func testHandler() (*Handler, *http.ServeMux) {
	handler := New(
		ascii.NewGenerator("../.."),
		ai.NewClient("", ""),
		"../../templates",
		log.New(io.Discard, "", 0),
	)
	mux := http.NewServeMux()
	handler.Register(mux)
	return handler, mux
}

func TestHome(t *testing.T) {
	_, mux := testHandler()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("код %d, ожидался 200", response.Code)
	}
	if !strings.Contains(response.Body.String(), "Symbol-Web") {
		t.Fatal("главная страница не содержит заголовок")
	}
}

func TestSymbolArt(t *testing.T) {
	_, mux := testHandler()
	form := url.Values{"text": {"Hello"}, "banner": {"standard"}}
	request := httptest.NewRequest(http.MethodPost, "/symbol-art", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("код %d, ожидался 200; ответ: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "ascii-result") {
		t.Fatal("страница не содержит результат ASCII-арта")
	}
}

func TestSymbolArtRejectsInvalidInput(t *testing.T) {
	_, mux := testHandler()
	tests := []url.Values{
		{"text": {""}, "banner": {"standard"}},
		{"text": {"Hello"}, "banner": {"other"}},
		{"text": {"Привет"}, "banner": {"standard"}},
	}
	for _, form := range tests {
		request := httptest.NewRequest(http.MethodPost, "/symbol-art", strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Errorf("для %v код %d, ожидался 400", form, response.Code)
		}
	}
}

func TestSuggestMockMode(t *testing.T) {
	_, mux := testHandler()
	request := httptest.NewRequest(http.MethodPost, "/api/suggest", strings.NewReader(`{"text":"Happy Birth"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("код %d, ожидался 200; ответ: %s", response.Code, response.Body.String())
	}
	var payload struct {
		Suggestions []string `json:"suggestions"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("ошибка чтения JSON: %v", err)
	}
	if len(payload.Suggestions) != 3 {
		t.Fatalf("получено %d подсказок, ожидалось 3", len(payload.Suggestions))
	}
}

func TestSuggestRequiresThreeCharacters(t *testing.T) {
	_, mux := testHandler()
	request := httptest.NewRequest(http.MethodPost, "/api/suggest", strings.NewReader(`{"text":"Hi"}`))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("код %d, ожидался 400", response.Code)
	}
}

func TestRecommendBanner(t *testing.T) {
	_, mux := testHandler()
	request := httptest.NewRequest(http.MethodPost, "/api/recommend-banner", strings.NewReader(`{"text":"WELCOME"}`))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("код %d, ожидался 200", response.Code)
	}
	var payload ai.Recommendation
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("ошибка чтения JSON: %v", err)
	}
	if payload.Recommended != "shadow" {
		t.Fatalf("получен %q, ожидался shadow", payload.Recommended)
	}
}

func TestMissingBannerReturnsNotFound(t *testing.T) {
	handler := New(
		ascii.NewGenerator(t.TempDir()),
		ai.NewClient("", ""),
		"../../templates",
		log.New(io.Discard, "", 0),
	)
	form := url.Values{"text": {"Hello"}, "banner": {"standard"}}
	request := httptest.NewRequest(http.MethodPost, "/symbol-art", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	handler.SymbolArt(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("код %d, ожидался 404", response.Code)
	}
}

func TestMissingTemplateReturnsNotFound(t *testing.T) {
	handler := New(
		ascii.NewGenerator("../.."),
		ai.NewClient("", ""),
		t.TempDir(),
		log.New(io.Discard, "", 0),
	)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	handler.Home(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("код %d, ожидался 404", response.Code)
	}
}

func TestSuggestReturnsServiceUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	handler := New(
		ascii.NewGenerator("../.."),
		ai.NewClient(server.URL, "test-model"),
		"../../templates",
		log.New(io.Discard, "", 0),
	)
	request := httptest.NewRequest(http.MethodPost, "/api/suggest", strings.NewReader(`{"text":"Hello"}`))
	response := httptest.NewRecorder()
	handler.Suggest(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("код %d, ожидался 503", response.Code)
	}
}
