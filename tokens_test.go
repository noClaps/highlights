package highlights

import (
	"os"
	"sync"
	"testing"
)

// Lines share one token array, so appending to a line must not overwrite
// the next.
func TestTokenLinesAppendIndependently(t *testing.T) {
	theme, err := os.ReadFile("themes/data/pierre-dark.json")
	if err != nil {
		t.Fatal(err)
	}
	res, err := CodeToTokens("a := 1\nb := 2\n", Options{Lang: "go", ThemeOptions: ThemeOptions{Theme: MustParseTheme(theme)}})
	if err != nil {
		t.Fatal(err)
	}
	next := res.Tokens[1][0].Content
	_ = append(res.Tokens[0], ThemedToken{Content: "x"})
	if got := res.Tokens[1][0].Content; got != next {
		t.Fatalf("appending to line 0 changed line 1 from %q to %q", next, got)
	}
}

// The package-level functions share theme sets and their style tables
// across goroutines; run with -race.
func TestThemeSetsConcurrent(t *testing.T) {
	data, err := os.ReadFile("themes/data/pierre-dark.json")
	if err != nil {
		t.Fatal(err)
	}
	theme := MustParseTheme(data)
	opts := Options{Lang: "go", ThemeOptions: ThemeOptions{
		Themes:            map[string]ThemeSource{"light": theme, "dark": theme},
		CSSVariablePrefix: "--concurrent-",
	}}
	const code = "func f() string { return \"x\" }\n"
	// the goroutines race to build the theme set and its style table
	results := make([]*TokensResult, 8)
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() {
			for range 50 {
				res, err := CodeToTokens(code, opts)
				if err != nil {
					t.Error(err)
					return
				}
				results[i] = res
				if _, err := CodeToHTML(code, opts); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
	want, err := CodeToTokens(code, opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range results {
		if got == nil || got.ThemeName != want.ThemeName || got.Fg != want.Fg || len(got.Tokens[0]) != len(want.Tokens[0]) {
			t.Fatalf("tokens differ: %+v, want %+v", got, want)
		}
	}
}
