package ascii

import (
	"strings"
	"testing"
)

func FuzzGenerate(f *testing.F) {
	for _, seed := range []struct{ text, banner string }{
		{"{123}\n<Hello> (World)!", "standard"}, {"123 T/fs#R", "thinkertoy"},
		{"WELCOME", "shadow"}, {"\n\r\n", "standard"}, {`A\nB`, "standard"},
		{"Привет", "shadow"}, {"\x00\xff", "standard"}, {"Hello", "../../etc/passwd"},
		{strings.Repeat("W", 1000), "shadow"},
	} {
		f.Add(seed.text, seed.banner)
	}
	generator := NewGenerator("../..")
	f.Fuzz(func(t *testing.T, text, banner string) {
		if len(text) > 4096 || len(banner) > 128 {
			t.Skip()
		}
		art, err := generator.Generate(text, banner)
		if err != nil {
			if art != "" {
				t.Fatal("failed generation returned partial art")
			}
			return
		}
		if ValidateText(text, false) != nil || !IsBanner(banner) {
			t.Fatal("invalid input accepted")
		}
		if len(art) > 170000 {
			t.Fatalf("unexpected output amplification: %d bytes", len(art))
		}
		for _, char := range art {
			if char != '\n' && (char < ' ' || char > '~') {
				t.Fatalf("invalid output character %U", char)
			}
		}
		repeat, repeatErr := generator.Generate(text, banner)
		if repeatErr != nil || repeat != art {
			t.Fatal("rendering is not deterministic")
		}
	})
}
