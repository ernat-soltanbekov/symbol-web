package ascii

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	characterHeight = 8
	firstASCII      = 32
	lastASCII       = 126
	linesPerGlyph   = characterHeight + 1
	expectedLines   = (lastASCII - firstASCII + 1) * linesPerGlyph
	maxTextLength   = 1000
)

var (
	ErrEmptyText       = errors.New("текст не должен быть пустым")
	ErrTextTooLong     = errors.New("текст превышает 1000 символов")
	ErrInvalidText     = errors.New("текст содержит недопустимые символы")
	ErrInvalidBanner   = errors.New("неизвестный баннер")
	ErrBannerNotFound  = errors.New("файл баннера не найден")
	ErrMalformedBanner = errors.New("неверный формат файла баннера")
)

// Generator загружает стандартные баннеры из заданного каталога.
type Generator struct {
	directory string
}

// NewGenerator создаёт генератор, который читает баннеры из каталога.
func NewGenerator(directory string) *Generator {
	return &Generator{directory: directory}
}

// IsBanner проверяет, входит ли имя в список поддерживаемых баннеров.
func IsBanner(name string) bool {
	switch name {
	case "standard", "shadow", "thinkertoy":
		return true
	default:
		return false
	}
}

// ValidateText проверяет длину и допустимый диапазон символов.
func ValidateText(text string, allowEmpty bool) error {
	if text == "" && !allowEmpty {
		return ErrEmptyText
	}
	if utf8.RuneCountInString(text) > maxTextLength {
		return ErrTextTooLong
	}
	for _, char := range text {
		if char == '\n' || char == '\r' {
			continue
		}
		if char < firstASCII || char > lastASCII {
			return ErrInvalidText
		}
	}
	return nil
}

// Generate создаёт ASCII-арт для текста и выбранного баннера.
func (g *Generator) Generate(text, banner string) (string, error) {
	if err := ValidateText(text, false); err != nil {
		return "", err
	}
	if !IsBanner(banner) {
		return "", ErrInvalidBanner
	}

	glyphs, err := g.loadBanner(banner)
	if err != nil {
		return "", err
	}

	// Поле формы может передать как настоящий перевод строки, так и последовательность \n.
	text = strings.ReplaceAll(text, "\\n", "\n")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")

	var result strings.Builder
	seenNonEmpty := false
	for lineIndex, line := range lines {
		if line != "" {
			seenNonEmpty = true
			for row := 0; row < characterHeight; row++ {
				for _, char := range line {
					result.WriteString(glyphs[char-firstASCII][row])
				}
				result.WriteByte('\n')
			}
		}

		// Пустая строка между частями текста должна остаться пустой строкой в арте.
		if line == "" && lineIndex < len(lines)-1 {
			result.WriteByte('\n')
		}
		if lineIndex == len(lines)-1 && line == "" && seenNonEmpty {
			result.WriteByte('\n')
		}
	}

	return result.String(), nil
}

func (g *Generator) loadBanner(name string) ([][]string, error) {
	// Файлы небольшие. Читаем их заново, чтобы удалённый или повреждённый
	// баннер сразу возвращал ошибку, в том числе после успешного запроса.
	path := filepath.Join(g.directory, name+".txt")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrBannerNotFound, name)
		}
		return nil, fmt.Errorf("чтение баннера %s: %w", name, err)
	}

	content := strings.ReplaceAll(string(data), "\r\n", "\n")
	content = strings.TrimSuffix(content, "\n")
	lines := strings.Split(content, "\n")
	if len(lines) != expectedLines {
		return nil, fmt.Errorf("%w: ожидалось %d строк, получено %d", ErrMalformedBanner, expectedLines, len(lines))
	}

	glyphs := make([][]string, lastASCII-firstASCII+1)
	for index := range glyphs {
		start := 1 + index*linesPerGlyph
		if lines[start-1] != "" {
			return nil, fmt.Errorf("%w: отсутствует разделитель перед символом %q", ErrMalformedBanner, rune(index+firstASCII))
		}
		rows := lines[start : start+characterHeight]
		width := len(rows[0])
		for _, row := range rows {
			if width == 0 || len(row) != width {
				return nil, fmt.Errorf("%w: разная ширина строк символа %q", ErrMalformedBanner, rune(index+firstASCII))
			}
			for _, char := range row {
				if char < firstASCII || char > lastASCII {
					return nil, fmt.Errorf("%w: недопустимый знак в символе %q", ErrMalformedBanner, rune(index+firstASCII))
				}
			}
		}
		glyphs[index] = rows
	}
	return glyphs, nil
}
