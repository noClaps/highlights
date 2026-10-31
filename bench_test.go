package highlights

import (
	"os"
	"strings"
	"testing"
)

func BenchmarkCodeToHTML(b *testing.B) {
	data, err := os.ReadFile("vendor/pierre-highlights/lib/live.ts")
	if err != nil {
		b.Fatal(err)
	}
	theme, _ := os.ReadFile("themes/data/pierre-dark.json")
	opts := Options{Lang: "ts", ThemeOptions: ThemeOptions{Theme: MustParseTheme(theme)}}
	code := strings.Repeat(string(data), 4)
	h := NewHighlighter()
	b.SetBytes(int64(len(code)))
	for b.Loop() {
		if _, err := h.CodeToHTML(code, opts); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCodeToTokens(b *testing.B) {
	data, err := os.ReadFile("vendor/pierre-highlights/lib/live.ts")
	if err != nil {
		b.Fatal(err)
	}
	theme, _ := os.ReadFile("themes/data/pierre-dark.json")
	opts := Options{Lang: "ts", ThemeOptions: ThemeOptions{Theme: MustParseTheme(theme)}}
	code := strings.Repeat(string(data), 4)
	h := NewHighlighter()
	b.SetBytes(int64(len(code)))
	for b.Loop() {
		if _, err := h.CodeToTokens(code, opts); err != nil {
			b.Fatal(err)
		}
	}
}
