package handlers

import (
	"bytes"
	"errors"
	"html/template"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

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
	Result   string `json:"result"`
	Text     string `json:"text"`
	Banner   string `json:"banner"`
	MockMode bool   `json:"-"`
}

// New создаёт обработчик с переданными зависимостями.
func New(generator *ascii.Generator, aiClient *ai.Client, templateDir string, logger *log.Logger) *Handler {
	if logger == nil {
		logger = log.Default()
	}
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
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.writeJSONError(w, http.StatusNotFound, "API-маршрут не найден")
			return
		}
		h.renderError(w, http.StatusNotFound, "Страница не найдена")
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		h.renderError(w, http.StatusMethodNotAllowed, "Метод не поддерживается")
		return
	}
	h.renderPage(w, "index.html", PageData{Banner: "standard", MockMode: h.aiClient.MockMode()})
}

// SymbolArt обрабатывает форму и отображает созданный ASCII-арт.
func (h *Handler) SymbolArt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		h.formError(w, r, http.StatusMethodNotAllowed, "Метод не поддерживается")
		return
	}

	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" {
		h.formError(w, r, http.StatusBadRequest, "Отправьте текст и баннер через HTML-форму")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		h.formError(w, r, http.StatusBadRequest, "Некорректные данные формы")
		return
	}
	if len(r.PostForm["text"]) != 1 || len(r.PostForm["banner"]) != 1 {
		h.formError(w, r, http.StatusBadRequest, "Укажите текст и один баннер")
		return
	}
	text := r.PostForm.Get("text")
	banner := r.PostForm.Get("banner")

	result, err := h.generator.Generate(text, banner)
	if err != nil {
		switch {
		case errors.Is(err, ascii.ErrEmptyText), errors.Is(err, ascii.ErrTextTooLong), errors.Is(err, ascii.ErrInvalidText), errors.Is(err, ascii.ErrInvalidBanner):
			h.formError(w, r, http.StatusBadRequest, err.Error())
		case errors.Is(err, ascii.ErrBannerNotFound):
			h.formError(w, r, http.StatusNotFound, "Файл баннера не найден")
		default:
			h.logger.Printf("ошибка генерации ASCII-арта: %v", err)
			h.formError(w, r, http.StatusInternalServerError, "Не удалось создать ASCII-арт")
		}
		return
	}

	data := PageData{Result: result, Text: text, Banner: banner, MockMode: h.aiClient.MockMode()}
	if acceptsJSON(r) {
		h.writeJSON(w, http.StatusOK, data)
		return
	}
	h.renderPage(w, "index.html", data)
}

func acceptsJSON(r *http.Request) bool {
	for _, value := range strings.Split(r.Header.Get("Accept"), ",") {
		mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(value))
		if err == nil && mediaType == "application/json" && params["q"] != "0" {
			return true
		}
	}
	return false
}

func (h *Handler) formError(w http.ResponseWriter, r *http.Request, status int, message string) {
	if acceptsJSON(r) {
		h.writeJSONError(w, status, message)
		return
	}
	h.renderError(w, status, message)
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
	paths := []string{filepath.Join(h.templateDir, name)}
	if name == "index.html" {
		paths = append(paths, filepath.Join(h.templateDir, "result.html"))
	}
	parsed, err := template.ParseFiles(paths...)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errTemplateNotFound
		}
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
