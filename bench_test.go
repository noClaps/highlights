package highlights

import (
	"os"
	"path/filepath"
	"testing"
)

// benchInputs are about 256 KB of real code per language, from this
// repository.
var benchInputs = []struct {
	name, lang string
	files      []string
}{
	{"go", "go", []string{"*.go"}},
	{"ts", "ts", []string{"tools/record/ts/lib/*.ts"}},
	{"python", "python", []string{"tools/record/instrument.py"}},
	{"json", "json", []string{"themes/data/*.json"}},
	{"markdown", "markdown", []string{"README.md", "testdata/golden/README.md"}},
	{"wat", "wat", []string{"internal/engine/wat/*.wat", "internal/engine/wat/langs/*.wat"}},
}

const benchSize = 256 << 10

func benchInput(b *testing.B, patterns []string) string {
	var files []string
	for _, p := range patterns {
		m, err := filepath.Glob(p)
		if err != nil || len(m) == 0 {
			b.Fatalf("no files for %s", p)
		}
		files = append(files, m...)
	}
	var code []byte
	for len(code) < benchSize {
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				b.Fatal(err)
			}
			code = append(code, data...)
			if len(code) >= benchSize {
				break
			}
		}
	}
	return string(code)
}

func benchOptions(b *testing.B, lang string) Options {
	theme, err := os.ReadFile("themes/data/pierre-dark.json")
	if err != nil {
		b.Fatal(err)
	}
	return Options{Lang: lang, ThemeOptions: ThemeOptions{Theme: MustParseTheme(theme)}}
}

func BenchmarkCodeToHTML(b *testing.B) {
	for _, in := range benchInputs {
		b.Run(in.name, func(b *testing.B) {
			code, opts := benchInput(b, in.files), benchOptions(b, in.lang)
			h := NewHighlighter()
			b.SetBytes(int64(len(code)))
			for b.Loop() {
				if _, err := h.CodeToHTML(code, opts); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkCodeToTokens(b *testing.B) {
	for _, in := range benchInputs {
		b.Run(in.name, func(b *testing.B) {
			code, opts := benchInput(b, in.files), benchOptions(b, in.lang)
			h := NewHighlighter()
			b.SetBytes(int64(len(code)))
			for b.Loop() {
				if _, err := h.CodeToTokens(code, opts); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
