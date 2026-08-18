package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"symbol-web/internal/ai"
	"symbol-web/internal/ascii"
)

type textRequest struct {
	Text string `json:"text"`
}

// Suggest возвращает LLM-подсказки или их mock-версию.
func (h *Handler) Suggest(w http.ResponseWriter, r *http.Request) {
	if !h.requireJSONPost(w, r) {
		return
	}
	request, ok := h.decodeText(w, r)
	if !ok {
		return
	}
	if len([]rune(strings.TrimSpace(request.Text))) < 3 {
		h.writeJSONError(w, http.StatusBadRequest, "Введите не менее трёх символов")
		return
	}

	suggestions, err := h.aiClient.Suggestions(r.Context(), request.Text)
	if err != nil {
		h.writeAIError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"suggestions": suggestions})
}

// RecommendBanner возвращает результат анализа текста по правилам.
func (h *Handler) RecommendBanner(w http.ResponseWriter, r *http.Request) {
	if !h.requireJSONPost(w, r) {
		return
	}
	request, ok := h.decodeText(w, r)
	if !ok {
		return
	}
	h.writeJSON(w, http.StatusOK, ai.RecommendBanner(request.Text))
}

// Variations возвращает творческие варианты текста.
func (h *Handler) Variations(w http.ResponseWriter, r *http.Request) {
	if !h.requireJSONPost(w, r) {
		return
	}
	request, ok := h.decodeText(w, r)
	if !ok {
		return
	}

	variations, err := h.aiClient.Variations(r.Context(), request.Text)
	if err != nil {
		h.writeAIError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"variations": variations})
}

func (h *Handler) requireJSONPost(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		h.writeJSONError(w, http.StatusMethodNotAllowed, "Метод не поддерживается")
		return false
	}
	return true
}

func (h *Handler) decodeText(w http.ResponseWriter, r *http.Request) (textRequest, bool) {
	var request textRequest
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		h.writeJSONError(w, http.StatusBadRequest, "Некорректный JSON-запрос")
		return request, false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		h.writeJSONError(w, http.StatusBadRequest, "JSON-запрос должен содержать один объект")
		return request, false
	}
	if err := ascii.ValidateText(request.Text, false); err != nil {
		h.writeJSONError(w, http.StatusBadRequest, err.Error())
		return request, false
	}
	return request, true
}

func (h *Handler) writeAIError(w http.ResponseWriter, err error) {
	h.logger.Printf("ошибка LLM: %v", err)
	if errors.Is(err, ai.ErrUnavailable) {
		h.writeJSONError(w, http.StatusServiceUnavailable, "AI-сервис временно недоступен")
		return
	}
	h.writeJSONError(w, http.StatusInternalServerError, "AI-сервис вернул некорректный ответ")
}

func (h *Handler) writeJSONError(w http.ResponseWriter, status int, message string) {
	h.writeJSON(w, status, map[string]string{"error": message})
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		h.logger.Printf("ошибка записи JSON-ответа: %v", err)
	}
}
