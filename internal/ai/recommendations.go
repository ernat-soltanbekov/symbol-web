package ai

import "strings"

// Alternative описывает запасной баннер и его оценку.
type Alternative struct {
	Banner string  `json:"banner"`
	Score  float64 `json:"score"`
	Reason string  `json:"reason"`
}

// Recommendation содержит результат детерминированного анализа текста.
type Recommendation struct {
	Recommended  string        `json:"recommended"`
	Reasoning    string        `json:"reasoning"`
	Alternatives []Alternative `json:"alternatives"`
}

// RecommendBanner выбирает баннер по длине, регистру и специальным символам.
func RecommendBanner(text string) Recommendation {
	letters := 0
	upper := 0
	special := 0
	for _, char := range text {
		switch {
		case char >= 'A' && char <= 'Z':
			letters++
			upper++
		case char >= 'a' && char <= 'z':
			letters++
		case char != ' ' && (char < '0' || char > '9'):
			special++
		}
	}

	length := len([]rune(strings.TrimSpace(text)))
	allUpper := letters > 0 && upper == letters
	symbolHeavy := length > 0 && special*3 >= length

	switch {
	case symbolHeavy:
		return Recommendation{
			Recommended: "thinkertoy",
			Reasoning:   "Декоративный текст с символами лучше сочетается с игривым стилем thinkertoy.",
			Alternatives: []Alternative{
				{Banner: "standard", Score: 0.6, Reason: "Читаемый нейтральный вариант"},
				{Banner: "shadow", Score: 0.4, Reason: "Может выглядеть слишком тяжело"},
			},
		}
	case allUpper || length <= 6:
		return Recommendation{
			Recommended: "shadow",
			Reasoning:   "Короткий текст или верхний регистр заметнее выглядит в выразительном стиле shadow.",
			Alternatives: []Alternative{
				{Banner: "standard", Score: 0.7, Reason: "Хорошая читаемая альтернатива"},
				{Banner: "thinkertoy", Score: 0.4, Reason: "Менее выразителен для такого текста"},
			},
		}
	case length > 12:
		return Recommendation{
			Recommended: "standard",
			Reasoning:   "Длинный текст проще читать в сбалансированном стиле standard.",
			Alternatives: []Alternative{
				{Banner: "shadow", Score: 0.6, Reason: "Выразительно, но занимает больше места"},
				{Banner: "thinkertoy", Score: 0.5, Reason: "Подходит для более игривой подачи"},
			},
		}
	default:
		return Recommendation{
			Recommended: "thinkertoy",
			Reasoning:   "Смешанный короткий текст хорошо сочетается с лёгким стилем thinkertoy.",
			Alternatives: []Alternative{
				{Banner: "standard", Score: 0.7, Reason: "Нейтральный и читаемый вариант"},
				{Banner: "shadow", Score: 0.5, Reason: "Подходит, если нужен сильный акцент"},
			},
		}
	}
}
