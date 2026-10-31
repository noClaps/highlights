package highlights_test

import (
	"strings"
	"testing"

	"highlights"
	"highlights/themes"
)

// Lexer behavior that departs from the original @pierre/highlights; the
// README lists each departure.

// wordStyles returns the color of each whole word in code, in order.
func wordStyles(t *testing.T, lang, code string) [][2]string {
	t.Helper()
	res, err := highlights.CodeToTokens(code, highlights.Options{
		Lang:         lang,
		ThemeOptions: highlights.ThemeOptions{Theme: themes.PierreDark},
	})
	if err != nil {
		t.Fatal(err)
	}
	var words [][2]string
	for _, line := range res.Tokens {
		for _, tok := range line {
			for _, w := range strings.Fields(tok.Content) {
				words = append(words, [2]string{w, tok.Color})
			}
		}
	}
	return words
}

func TestPythonAsIsAKeyword(t *testing.T) {
	keyword := ""
	for _, w := range wordStyles(t, "python", "import x") {
		if w[0] == "import" {
			keyword = w[1]
		}
	}
	code := "import numpy as np\nwith open(p) as f:\n    pass\ntry:\n    pass\nexcept E as e:\n    pass\nimport asyncio\nhas = as_ + ask\n"
	for _, w := range wordStyles(t, "python", code) {
		switch strings.Trim(w[0], ":(),") {
		case "as":
			if w[1] != keyword {
				t.Errorf("as is %s, want the keyword color %s", w[1], keyword)
			}
		case "asyncio", "has", "as_", "ask":
			if w[1] == keyword {
				t.Errorf("%s has the keyword color", w[0])
			}
		}
	}
}
