package handlers

import (
	"bytes"
	"errors"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"symbol-web/internal/ai"
	"symbol-web/internal/ascii"
)

var errTemplateNotFound = errors.New("шаблон не найден")

// Handler объединяет HTML- и JSON-обработчики приложения.
type Handler struct {
	generator   *ascii.Generator
	aiClient    *ai.Client
	templateDir string
	logger      *log.Logger
}

// PageData содержит данные для главного шаблона.
type PageData struct {
	Result string
	Text   string
	Banner string
}

// New создаёт обработчик с переданными зависимостями.
func New(generator *ascii.Generator, aiClient *ai.Client, templateDir string, logger *log.Logger) *Handler {
	return &Handler{
		generator:   generator,
		aiClient:    aiClient,
		templateDir: templateDir,
		logger:      logger,
	}
}

// Register регистрирует все маршруты приложения.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/", h.Home)
	mux.HandleFunc("/symbol-art", h.SymbolArt)
	mux.HandleFunc("/api/suggest", h.Suggest)
	mux.HandleFunc("/api/recommend-banner", h.RecommendBanner)
	mux.HandleFunc("/api/variations", h.Variations)
}

// Home отображает главную страницу.
func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		h.renderError(w, http.StatusNotFound, "Страница не найдена")
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		h.renderError(w, http.StatusMethodNotAllowed, "Метод не поддерживается")
		return
	}
	h.renderPage(w, "index.html", PageData{Banner: "standard"})
}

// SymbolArt обрабатывает форму и отображает созданный ASCII-арт.
func (h *Handler) SymbolArt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		h.renderError(w, http.StatusMethodNotAllowed, "Метод не поддерживается")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		h.renderError(w, http.StatusBadRequest, "Некорректные данные формы")
		return
	}
	text := r.FormValue("text")
	banner := r.FormValue("banner")

	result, err := h.generator.Generate(text, banner)
	if err != nil {
		switch {
		case errors.Is(err, ascii.ErrEmptyText), errors.Is(err, ascii.ErrTextTooLong), errors.Is(err, ascii.ErrInvalidText), errors.Is(err, ascii.ErrInvalidBanner):
			h.renderError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, ascii.ErrBannerNotFound):
			h.renderError(w, http.StatusNotFound, "Файл баннера не найден")
		default:
			h.logger.Printf("ошибка генерации ASCII-арта: %v", err)
			h.renderError(w, http.StatusInternalServerError, "Не удалось создать ASCII-арт")
		}
		return
	}

	h.renderPage(w, "index.html", PageData{Result: result, Text: text, Banner: banner})
}

func (h *Handler) renderPage(w http.ResponseWriter, name string, data any) {
	content, err := h.executeTemplate(name, data)
	if err != nil {
		if errors.Is(err, errTemplateNotFound) {
			h.renderError(w, http.StatusNotFound, "Файл шаблона не найден")
			return
		}
		h.logger.Printf("ошибка шаблона %s: %v", name, err)
		h.renderError(w, http.StatusInternalServerError, "Ошибка отображения страницы")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

func (h *Handler) executeTemplate(name string, data any) ([]byte, error) {
	path := filepath.Join(h.templateDir, name)
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errTemplateNotFound
		}
		return nil, err
	}

	parsed, err := template.ParseFiles(path)
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	if err := parsed.ExecuteTemplate(&output, name, data); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func (h *Handler) renderError(w http.ResponseWriter, status int, message string) {
	data := struct {
		Status  int
		Message string
	}{Status: status, Message: message}
	content, err := h.executeTemplate("error.html", data)
	if err != nil {
		h.logger.Printf("ошибка шаблона error.html: %v", err)
		http.Error(w, message, status)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(content)
}
