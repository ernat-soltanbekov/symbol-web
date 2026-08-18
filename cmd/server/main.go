package main

import (
	"log"
	"net/http"
	"os"
	"symbol-web/internal/ai"
	"symbol-web/internal/ascii"
	"symbol-web/internal/handlers"
	"time"
)

func main() {
	logger := log.New(os.Stdout, "symbol-web: ", log.LstdFlags)
	mux := http.NewServeMux()

	generator := ascii.NewGenerator(".")
	aiClient := ai.NewClientFromEnv()
	handler := handlers.New(generator, aiClient, "templates", logger)
	handler.Register(mux)

	static := http.FileServer(http.Dir("static"))
	mux.Handle("/static/", http.StripPrefix("/static/", static))

	server := &http.Server{
		Addr:              ":8080",
		Handler:           logging(logger, mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	mode := "mock"
	if !aiClient.MockMode() {
		mode = "live"
	}
	logger.Printf("сервер запущен на http://localhost:8080 (AI: %s)", mode)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Fatalf("ошибка сервера: %v", err)
	}
}

func logging(logger *log.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		logger.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(started).Round(time.Millisecond))
	})
}
