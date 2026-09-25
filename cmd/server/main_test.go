package main

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStaticFiles(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "app.js"), []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	handler := staticFiles(directory)
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/app.js", 200}, {"HEAD", "/app.js", 200}, {"POST", "/app.js", 405},
		{"GET", "/", 404}, {"GET", "/missing", 404},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(tc.method, tc.path, nil))
		if response.Code != tc.want {
			t.Errorf("%s %s: %d, want %d", tc.method, tc.path, response.Code, tc.want)
		}
	}
}

func TestMiddlewareRecovery(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	handler := middleware(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { panic("simulated") }))
	for _, path := range []string{"/", "/api/suggest"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		if response.Code != 500 || !strings.Contains(response.Body.String(), "Внутренняя ошибка") {
			t.Fatalf("%s: %d %s", path, response.Code, response.Body.String())
		}
		if response.Header().Get("X-Content-Type-Options") != "nosniff" || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("missing response headers")
		}
		if path == "/api/suggest" && !strings.HasPrefix(response.Header().Get("Content-Type"), "application/json") {
			t.Fatal("panic API response isn't JSON")
		}
	}
}

func TestRunShutsDown(t *testing.T) {
	t.Setenv("ADDR", "127.0.0.1:0")
	t.Setenv("LLM_BASE_URL", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := run(ctx, log.New(io.Discard, "", 0)); err != nil {
		t.Fatal(err)
	}
}

func TestRunReportsInvalidAddress(t *testing.T) {
	t.Setenv("ADDR", "invalid::address")
	if err := run(context.Background(), log.New(io.Discard, "", 0)); err == nil {
		t.Fatal("expected listen error")
	}
}
