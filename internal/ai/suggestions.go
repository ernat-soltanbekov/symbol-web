package ai

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var listPrefix = regexp.MustCompile(`^\s*(?:[-*•]|\d+[.)])\s*`)

// Variation описывает творческий вариант текста для ASCII-арта.
type Variation struct {
	Text            string `json:"text"`
	Description     string `json:"description"`
	SuggestedBanner string `json:"suggested_banner"`
}

func suggestionPrompt(text string) string {
	return fmt.Sprintf("Complete this text creatively for ASCII art display:\nInput: %q\nProvide 3-5 short, creative completions suitable for ASCII art. Each completion must be under 50 characters. Return only the completions, one per line.", text)
}

func variationPrompt(text string) string {
	return fmt.Sprintf("Generate creative variations of this text for ASCII art:\nInput: %q\nCreate exactly 4 variations: professional, bold, friendly and decorative. For each provide text, description under 30 characters and suggested_banner (shadow, standard or thinkertoy). Return only a JSON array.", text)
}

func mockSuggestions(text string) []string {
	text = strings.TrimSpace(text)
	return []string{
		trimToRunes(text, 48) + "!",
		trimToRunes(text, 43) + " World",
		"~ " + trimToRunes(text, 45) + " ~",
	}
}

func mockVariations(text string) []Variation {
	text = strings.TrimSpace(text)
	return []Variation{
		{Text: trimToRunes(toTitle(text), 49), Description: "Деловой стиль", SuggestedBanner: "standard"},
		{Text: trimToRunes(strings.ToUpper(text)+"!", 49), Description: "Сильный акцент", SuggestedBanner: "shadow"},
		{Text: trimToRunes(text+" :) ", 49), Description: "Дружелюбный стиль", SuggestedBanner: "thinkertoy"},
		{Text: trimToRunes("~ "+text+" ~", 49), Description: "Декоративный стиль", SuggestedBanner: "thinkertoy"},
	}
}

func parseSuggestions(content string) ([]string, error) {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	seen := make(map[string]bool)
	result := make([]string, 0, 5)
	for _, line := range lines {
		line = listPrefix.ReplaceAllString(line, "")
		line = strings.Trim(strings.TrimSpace(line), "`\"'")
		if line == "" || len([]rune(line)) >= 50 || seen[line] {
			continue
		}
		seen[line] = true
		result = append(result, line)
		if len(result) == 5 {
			break
		}
	}
	if len(result) < 3 {
		return nil, fmt.Errorf("%w: получено меньше трёх подсказок", ErrInvalidResponse)
	}
	return result, nil
}

func parseVariations(content string) ([]Variation, error) {
	start := strings.Index(content, "[")
	end := strings.LastIndex(content, "]")
	if start < 0 || end < start {
		return nil, fmt.Errorf("%w: JSON-массив не найден", ErrInvalidResponse)
	}

	var variations []Variation
	if err := json.Unmarshal([]byte(content[start:end+1]), &variations); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidResponse, err)
	}
	if len(variations) < 3 || len(variations) > 5 {
		return nil, fmt.Errorf("%w: неверное количество вариантов", ErrInvalidResponse)
	}
	for _, variation := range variations {
		if strings.TrimSpace(variation.Text) == "" || strings.TrimSpace(variation.Description) == "" || !validBanner(variation.SuggestedBanner) {
			return nil, fmt.Errorf("%w: неполные данные варианта", ErrInvalidResponse)
		}
	}
	return variations, nil
}

func validBanner(name string) bool {
	return name == "standard" || name == "shadow" || name == "thinkertoy"
}

func trimToRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}

func toTitle(text string) string {
	if text == "" {
		return text
	}
	runes := []rune(strings.ToLower(text))
	if runes[0] >= 'a' && runes[0] <= 'z' {
		runes[0] -= 'a' - 'A'
	}
	return string(runes)
}
