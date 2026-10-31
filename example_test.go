package highlights_test

import (
	"errors"
	"fmt"
	"strings"

	"highlights"
	"highlights/themes"
)

func ExampleCodeToHTML() {
	html, err := highlights.CodeToHTML("x := 1", highlights.Options{
		Lang:         "go",
		ThemeOptions: highlights.ThemeOptions{Theme: themes.PierreDark},
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(html)
	// Output:
	// <pre class="highlights" style="background-color:#0a0a0a;color:#fafafa"><code><span style="color:#ffa359">x </span><span style="color:#636363">:</span><span style="color:#08c0ef">= </span><span style="color:#68cdf2">1</span></code></pre>
}

func ExampleCodeToTokens() {
	res, err := highlights.CodeToTokens("// hi\nreturn", highlights.Options{
		Lang:         "js",
		ThemeOptions: highlights.ThemeOptions{Theme: themes.PierreDark},
	})
	if err != nil {
		panic(err)
	}
	for i, line := range res.Tokens {
		for _, t := range line {
			fmt.Printf("line %d offset %d %q %s comment=%v\n", i, t.Offset, t.Content, t.Color, t.Type == highlights.TokenTypeComment)
		}
	}
	// Output:
	// line 0 offset 0 "// hi" #737373 comment=true
	// line 1 offset 6 "return" #ff678d comment=false
}

func ExampleCodeToHTML_themes() {
	html, err := highlights.CodeToHTML("1", highlights.Options{
		Lang: "json",
		ThemeOptions: highlights.ThemeOptions{Themes: map[string]highlights.ThemeSource{
			"light": themes.PierreLight,
			"dark":  themes.PierreDark,
		}},
	})
	if err != nil {
		panic(err)
	}
	// the light theme applies inline; the dark one is a custom property
	fmt.Println(strings.Contains(html, "--hls-dark:"))
	// Output: true
}

func ExampleStreamTokenizer() {
	st, err := highlights.NewStreamTokenizer(highlights.Options{
		Lang:         "python",
		ThemeOptions: highlights.ThemeOptions{Theme: themes.PierreDark},
	})
	if err != nil {
		panic(err)
	}
	for _, chunk := range []string{"x = '", "a'\ny", " = 2"} {
		lines, err := st.Push(chunk)
		if err != nil {
			panic(err)
		}
		for _, line := range lines {
			fmt.Println("completed line with", len(line), "tokens")
		}
	}
	rest, err := st.End()
	if err != nil {
		panic(err)
	}
	fmt.Println("final lines:", len(rest))
	// Output:
	// completed line with 3 tokens
	// final lines: 1
}

func ExampleLiveTokenizer() {
	lt, err := highlights.NewLiveTokenizer(highlights.LiveTokenizerOptions{
		Options: highlights.Options{
			Lang:         "go",
			ThemeOptions: highlights.ThemeOptions{Theme: themes.PierreDark},
		},
		Code: "a := 1\nb := 2",
	})
	if err != nil {
		panic(err)
	}
	defer lt.Close()
	update, err := lt.ApplyEdits([]highlights.TextEdit{{
		Range:   highlights.Range{Start: highlights.Position{Line: 1, Character: 5}, End: highlights.Position{Line: 1, Character: 6}},
		NewText: `"two"`,
	}}, nil)
	if err != nil {
		panic(err)
	}
	text, _ := lt.Text()
	fmt.Printf("revision %d: %q\n", update.Revision, text)
	tokens, _ := lt.LineTokens(1)
	last := tokens.Tokens[len(tokens.Tokens)-1]
	fmt.Println(last.Content, last.Type == highlights.TokenTypeString)
	// Output:
	// revision 1: "a := 1\nb := \"two\""
	// "two" true
}

func ExampleLangID() {
	_, err := highlights.LangID("klingon")
	fmt.Println(errors.Is(err, highlights.ErrUnknownLanguage), err)
	// Output: true unknown lang: klingon
}
