package highlights

// Ports of the linear-time checks in cpp_test.ts, kotlin_test.ts,
// markdown_test.ts, mdx_test.ts, and wolfram_test.ts. The golden replay
// checks their outputs; these check the time budgets.

import (
	"strings"
	"testing"
	"time"
)

func timed(t *testing.T, limit time.Duration, label string, f func()) {
	t.Helper()
	start := time.Now()
	f()
	if elapsed := time.Since(start); elapsed > limit {
		t.Errorf("%s took %v, limit %v", label, elapsed, limit)
	}
}

func htmlText(t *testing.T, html string) string {
	t.Helper()
	body := html[strings.Index(html, "<code>")+6 : strings.LastIndex(html, "</code>")]
	var sb strings.Builder
	for i := 0; i < len(body); {
		if body[i] == '<' {
			i += strings.IndexByte(body[i:], '>') + 1
			continue
		}
		sb.WriteByte(body[i])
		i++
	}
	return strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&").Replace(sb.String())
}

func TestCppASingleLongLineHighlightsInLinearTime(t *testing.T) {
	opts := Options{Lang: "cpp", ThemeOptions: ThemeOptions{Theme: pierreDarkForTest(t)}}
	unit := "int x = foo(a, b) + 1; "
	src := strings.Repeat(unit, (500_000+len(unit)-1)/len(unit))
	var html string
	timed(t, time.Second, "one long line", func() {
		var err error
		if html, err = NewHighlighter().CodeToHTML(src, opts); err != nil {
			t.Fatal(err)
		}
	})
	if htmlText(t, html) != src {
		t.Fatal("output is not lossless")
	}
}

func TestKotlinStringsWithoutTemplatesLexInLinearTime(t *testing.T) {
	opts := Options{Lang: "kotlin", ThemeOptions: ThemeOptions{Theme: pierreDarkForTest(t)}}
	code := strings.Repeat("val s = \"hello world\"\n", 20000)
	h := NewHighlighter()
	// warm up allocation paths the JavaScript test runs after a compile
	if _, err := h.CodeToTokens("val s = 1", opts); err != nil {
		t.Fatal(err)
	}
	var res *TokensResult
	timed(t, 100*time.Millisecond, "20k strings", func() {
		var err error
		if res, err = h.CodeToTokens(code, opts); err != nil {
			t.Fatal(err)
		}
	})
	if len(res.Tokens) != 20001 {
		t.Fatalf("%d lines", len(res.Tokens))
	}
}

func TestMarkdownLinesOfManyBracketsStayLinear(t *testing.T) {
	opts := Options{Lang: "markdown", ThemeOptions: ThemeOptions{Theme: pierreDarkForTest(t)}}
	for _, line := range []string{"<a", "[", "[a](", "[a] ", `<a "x>" `} {
		code := strings.Repeat(line, 80000) + "\n"
		timed(t, 250*time.Millisecond, line+" x80000", func() {
			html, err := NewHighlighter().CodeToHTML(code, opts)
			if err != nil || htmlText(t, html) != code {
				t.Fatalf("%q: %v", line, err)
			}
		})
	}
}

func TestMdxManyUnclosedTagsStayLinear(t *testing.T) {
	opts := Options{Lang: "mdx", ThemeOptions: ThemeOptions{Theme: pierreDarkForTest(t)}}
	for _, line := range []string{"a <b \n", "<a", "a <b c=\"x\" \n"} {
		code := strings.Repeat(line, 8000)
		timed(t, 250*time.Millisecond, line+" x8000", func() {
			html, err := NewHighlighter().CodeToHTML(code, opts)
			if err != nil || htmlText(t, html) != code {
				t.Fatalf("%q: %v", line, err)
			}
		})
	}
}

func TestWolframNestedCallsAfterStatementsStayLinear(t *testing.T) {
	opts := Options{Lang: "wolfram", ThemeOptions: ThemeOptions{Theme: pierreDarkForTest(t)}}
	for _, unit := range []string{"f[0;", "f[0/;", "f[{0;"} {
		n := 32000
		closing := "]"
		if strings.Contains(unit, "{") {
			closing = "}]"
		}
		code := strings.Repeat(unit, n) + "x" + strings.Repeat(closing, n)
		timed(t, 250*time.Millisecond, unit, func() {
			if _, err := NewHighlighter().CodeToTokens(code, opts); err != nil {
				t.Fatal(err)
			}
		})
	}
}
