package ascii

import (
	"errors"
	"strings"
	"testing"
)

func TestGenerateSupportsEveryBanner(t *testing.T) {
	generator := NewGenerator("../..")
	for _, banner := range []string{"standard", "shadow", "thinkertoy"} {
		t.Run(banner, func(t *testing.T) {
			art, err := generator.Generate("A", banner)
			if err != nil {
				t.Fatalf("Generate() вернул ошибку: %v", err)
			}
			if lines := strings.Count(art, "\n"); lines != 8 {
				t.Fatalf("получено %d строк, ожидалось 8", lines)
			}
			if strings.TrimSpace(art) == "" {
				t.Fatal("ASCII-арт не должен быть пустым")
			}
		})
	}
}

func TestGeneratePreservesNewlines(t *testing.T) {
	generator := NewGenerator("../..")
	tests := []struct {
		name  string
		text  string
		lines int
	}{
		{name: "одна строка", text: "A", lines: 8},
		{name: "две строки", text: "A\nB", lines: 16},
		{name: "пустая строка между словами", text: "A\n\nB", lines: 17},
		{name: "перевод строки в конце", text: "A\n", lines: 9},
		{name: "два перевода строки в конце", text: "A\n\n", lines: 10},
		{name: "буквальная последовательность", text: `A\nB`, lines: 16},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			art, err := generator.Generate(test.text, "standard")
			if err != nil {
				t.Fatalf("Generate() вернул ошибку: %v", err)
			}
			if lines := strings.Count(art, "\n"); lines != test.lines {
				t.Fatalf("получено %d строк, ожидалось %d", lines, test.lines)
			}
		})
	}
}

func TestGenerateValidation(t *testing.T) {
	generator := NewGenerator("../..")
	tests := []struct {
		name   string
		text   string
		banner string
		target error
	}{
		{name: "пустой текст", text: "", banner: "standard", target: ErrEmptyText},
		{name: "неизвестный баннер", text: "Hello", banner: "other", target: ErrInvalidBanner},
		{name: "не ASCII", text: "Привет", banner: "standard", target: ErrInvalidText},
		{name: "слишком длинный", text: strings.Repeat("a", 1001), banner: "standard", target: ErrTextTooLong},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := generator.Generate(test.text, test.banner)
			if !errors.Is(err, test.target) {
				t.Fatalf("ошибка %v, ожидалась %v", err, test.target)
			}
		})
	}
}

func TestMissingBannerFile(t *testing.T) {
	generator := NewGenerator(t.TempDir())
	_, err := generator.Generate("Hello", "standard")
	if !errors.Is(err, ErrBannerNotFound) {
		t.Fatalf("ошибка %v, ожидалась ErrBannerNotFound", err)
	}
}
