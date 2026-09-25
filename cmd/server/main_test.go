package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestMiddlewareRecoveryDiscardsStaleRepresentationHeaders(t *testing.T) {
	for _, headers := range []struct {
		name   string
		values http.Header
	}{
		{"short length", http.Header{"Content-Length": {"1"}}},
		{"long length", http.Header{"Content-Length": {"9999"}}},
		{"encoding", http.Header{"Content-Encoding": {"gzip"}}},
		{"trailer", http.Header{"Trailer": {"X-Previous-Result"}}},
	} {
		for _, path := range []string{"/", "/api/suggest"} {
			t.Run(headers.name+path, func(t *testing.T) {
				handler := middleware(log.New(io.Discard, "", 0), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					for name, values := range headers.values {
						w.Header()[name] = append([]string(nil), values...)
					}
					panic("failed before writing the original representation")
				}))
				server := httptest.NewServer(handler)
				defer server.Close()
				client := server.Client()
				client.Timeout = time.Second
				request, err := http.NewRequest(http.MethodGet, server.URL+path, nil)
				if err != nil {
					t.Fatal(err)
				}
				// Inspect the actual response encoding without transparent decompression.
				request.Header.Set("Accept-Encoding", "identity")
				response, err := client.Do(request)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				body, err := io.ReadAll(response.Body)
				if err != nil {
					t.Fatalf("recovered response is incomplete: %v", err)
				}
				if response.StatusCode != http.StatusInternalServerError || !strings.Contains(string(body), "Внутренняя ошибка сервера") {
					t.Fatalf("incorrect recovered response: status=%d, body=%q", response.StatusCode, body)
				}
				if response.Header.Get("Content-Encoding") != "" || len(response.Trailer) != 0 {
					t.Fatalf("recovered response kept stale metadata: headers=%v, trailers=%v", response.Header, response.Trailer)
				}
				if strings.HasPrefix(path, "/api/") && !json.Valid(body) {
					t.Fatalf("API recovery did not return valid JSON: %q", body)
				}
			})
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

func TestRecoveryAfterPartialWriteAbortsResponse(t *testing.T) {
	handler := middleware(log.New(io.Discard, "", 0), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"result":"partial`)
		if err := http.NewResponseController(w).Flush(); err != nil {
			panic(err)
		}
		panic("failure after partial write")
	}))
	server := httptest.NewServer(handler)
	defer server.Close()
	response, err := server.Client().Get(server.URL + "/api/suggest")
	if err != nil {
		return
	} // An aborted connection can fail before headers reach the client.
	defer response.Body.Close()
	_, err = io.ReadAll(response.Body)
	if err == nil {
		t.Fatal("partially written response completed successfully after panic")
	}
}

func TestMiddlewarePreservesExplicitAbort(t *testing.T) {
	handler := middleware(log.New(io.Discard, "", 0), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { panic(http.ErrAbortHandler) }))
	defer func() {
		if recovered := recover(); recovered != http.ErrAbortHandler {
			t.Fatalf("expected HTTP abort panic, got %v", recovered)
		}
	}()
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
}
