package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"symbol-web/internal/ai"
	"symbol-web/internal/ascii"
	"symbol-web/internal/handlers"
)

func main() {
	logger := log.New(os.Stdout, "symbol-web: ", log.LstdFlags)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, logger); err != nil {
		logger.Printf("ошибка сервера: %v", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *log.Logger) error {
	client := ai.NewClientFromEnv()
	mux := http.NewServeMux()
	handlers.New(ascii.NewGenerator("."), client, "templates", logger).Register(mux)
	mux.Handle("/static/", http.StripPrefix("/static/", staticFiles("static")))
	addr := strings.TrimSpace(os.Getenv("ADDR"))
	if addr == "" {
		addr = ":8080"
	}
	server := &http.Server{
		Addr:              addr,
		Handler:           middleware(logger, mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}

	stopped := make(chan error, 1)
	go func() { stopped <- server.ListenAndServe() }()
	mode := "mock"
	if !client.MockMode() {
		mode = "live"
	}
	logger.Printf("адрес %s (AI: %s); по умолчанию http://localhost:8080", addr, mode)
	select {
	case err := <-stopped:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return fmt.Errorf("завершение запросов: %w", err)
		}
		if err := <-stopped; !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		logger.Print("сервер остановлен")
		return nil
	}
}

// Files are public assets, but directory indexes are not part of the site.
func staticFiles(directory string) http.Handler {
	root := http.Dir(directory)
	files := http.FileServer(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Метод не поддерживается", http.StatusMethodNotAllowed)
			return
		}
		file, err := root.Open(r.URL.Path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		info, err := file.Stat()
		_ = file.Close()
		if err != nil || !info.Mode().IsRegular() {
			http.NotFound(w, r)
			return
		}
		files.ServeHTTP(w, r)
	})
}

func middleware(logger *log.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'")
		if !strings.HasPrefix(r.URL.Path, "/static/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		started := time.Now()
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Printf("panic while serving %s: %v", r.URL.Path, recovered)
				if strings.HasPrefix(r.URL.Path, "/api/") || strings.Contains(r.Header.Get("Accept"), "application/json") {
					w.Header().Set("Content-Type", "application/json; charset=utf-8")
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte("{\"error\":\"Внутренняя ошибка сервера\"}\n"))
				} else {
					http.Error(w, "Внутренняя ошибка сервера", http.StatusInternalServerError)
				}
			}
			logger.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(started).Round(time.Millisecond))
		}()
		next.ServeHTTP(w, r)
	})
}
