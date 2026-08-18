package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestMockSuggestionsAreDeterministic(t *testing.T) {
	client := NewClient("", "")
	first, err := client.Suggestions(context.Background(), "Happy Birth")
	if err != nil {
		t.Fatalf("Suggestions() вернул ошибку: %v", err)
	}
	second, err := client.Suggestions(context.Background(), "Happy Birth")
	if err != nil {
		t.Fatalf("Suggestions() вернул ошибку: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("mock-ответы различаются: %v и %v", first, second)
	}
	if len(first) != 3 {
		t.Fatalf("получено %d подсказок, ожидалось 3", len(first))
	}
}

func TestLiveOllamaSuggestions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			t.Fatalf("путь %q, ожидался /api/generate", r.URL.Path)
		}
		var request struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("ошибка чтения запроса: %v", err)
		}
		if request.Model != "test-model" {
			t.Fatalf("модель %q, ожидалась test-model", request.Model)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"response": "- Hello!\n- Hello World\n- Hello Team",
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-model")
	suggestions, err := client.Suggestions(context.Background(), "Hello")
	if err != nil {
		t.Fatalf("Suggestions() вернул ошибку: %v", err)
	}
	if len(suggestions) != 3 || suggestions[0] != "Hello!" {
		t.Fatalf("неожиданные подсказки: %v", suggestions)
	}
}

func TestRecommendBannerRules(t *testing.T) {
	tests := []struct {
		text string
		want string
	}{
		{text: "WELCOME", want: "shadow"},
		{text: "This is a long message", want: "standard"},
		{text: "HelloWorld", want: "thinkertoy"},
		{text: "*** wow ***", want: "thinkertoy"},
	}
	for _, test := range tests {
		recommendation := RecommendBanner(test.text)
		if recommendation.Recommended != test.want {
			t.Errorf("для %q получен %q, ожидался %q", test.text, recommendation.Recommended, test.want)
		}
		if len(recommendation.Alternatives) != 2 {
			t.Errorf("для %q ожидалось две альтернативы", test.text)
		}
	}
}

func TestMockVariations(t *testing.T) {
	client := NewClient("", "")
	variations, err := client.Variations(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Variations() вернул ошибку: %v", err)
	}
	if len(variations) != 4 {
		t.Fatalf("получено %d вариантов, ожидалось 4", len(variations))
	}
	for _, variation := range variations {
		if !validBanner(variation.SuggestedBanner) {
			t.Errorf("неверный баннер %q", variation.SuggestedBanner)
		}
	}
}
