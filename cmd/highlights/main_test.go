package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// call runs the command and returns its exit status and output.
func call(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	status := run(args, strings.NewReader(stdin), &stdout, &stderr)
	return status, stdout.String(), stderr.String()
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

func TestHighlightsStandardInput(t *testing.T) {
	code := "def f(x):\n    return x  # hi\n"
	status, out, errOut := call(t, code, "-l", "python")
	if status != 0 {
		t.Fatalf("status %d: %s", status, errOut)
	}
	if !strings.HasPrefix(out, `<pre class="highlights" style="background-color:#0a0a0a;color:#fafafa"><code>`) || !strings.HasSuffix(out, "</code></pre>\n") {
		t.Errorf("not a pierre-dark HTML block: %q", out)
	}
	if got := tagRe.ReplaceAllString(out, ""); got != code+"\n" {
		t.Errorf("text without tags = %q, want the input %q", got, code)
	}
	// pierre-dark colors the keyword and the comment differently
	if !strings.Contains(out, `<span style="color:#ff678d">def `) || !strings.Contains(out, `<span style="color:#737373"># hi`) {
		t.Errorf("missing keyword or comment colors in %q", out)
	}
}

func TestHighlightsAFileByItsName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	status, out, errOut := call(t, "", path)
	if status != 0 {
		t.Fatalf("status %d: %s", status, errOut)
	}
	if !strings.Contains(out, `<span style="color:#ff678d">package </span>`) {
		t.Errorf("Go keyword not colored: %q", out)
	}
}

func TestDetectsTheLanguageFromTheFileName(t *testing.T) {
	for path, want := range map[string]string{
		"main.go":             "go",
		"src/App.test.tsx":    "tsx",
		"Dockerfile":          "dockerfile",
		".github/CODEOWNERS":  "codeowners",
		"notes.unknownformat": "text",
		"-":                   "text",
	} {
		if got := detectLanguage(path); got != want {
			t.Errorf("detectLanguage(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestUsesTheChosenTheme(t *testing.T) {
	_, out, _ := call(t, "x := 1", "-l", "go", "-t", "github-light")
	if !strings.HasPrefix(out, `<pre class="highlights" style="background-color:#ffffff;color:#24292e">`) {
		t.Errorf("not a github-light block: %q", out)
	}
}

func TestReadsAThemeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mine.json")
	theme := `{"name": "Mine", "appearance": "dark", "style": {"editor.foreground": "#123456", "syntax": {"keyword": {"color": "#abcdef", "font_weight": 700}}}}`
	if err := os.WriteFile(path, []byte(theme), 0o644); err != nil {
		t.Fatal(err)
	}
	status, out, errOut := call(t, "return", "-l", "js", "-t", path)
	if status != 0 {
		t.Fatalf("status %d: %s", status, errOut)
	}
	if !strings.Contains(out, `<span style="color:#abcdef;font-weight:700">return</span>`) {
		t.Errorf("keyword not styled by the theme file: %q", out)
	}
}

func TestReportsUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"-l", "nosuchlanguage"},
		{"-t", "nosuchtheme"},
		{"a.go", "b.go"},
		{"-nosuchflag"},
	} {
		status, out, errOut := call(t, "x", args...)
		if status != 2 || out != "" || errOut == "" {
			t.Errorf("%v: status %d, stdout %q, stderr %q; want status 2 and a message", args, status, out, errOut)
		}
	}
}

func TestReportsAMissingFile(t *testing.T) {
	status, _, errOut := call(t, "", filepath.Join(t.TempDir(), "missing.go"))
	if status != 1 || !strings.Contains(errOut, "missing.go") {
		t.Errorf("status %d, stderr %q; want status 1 naming the file", status, errOut)
	}
}

func TestListsLanguagesAndThemes(t *testing.T) {
	_, out, _ := call(t, "", "-list-langs")
	if !regexp.MustCompile(`(?m)^.*\bgo\b.*$`).MatchString(out) || !strings.Contains(out, "python") {
		t.Errorf("languages missing go or python:\n%s", out)
	}
	_, out, _ = call(t, "", "-list-themes")
	if !strings.Contains(out, "pierre-dark\n") || !strings.Contains(out, "github-light\n") {
		t.Errorf("themes missing pierre-dark or github-light:\n%s", out)
	}
}

func BenchmarkHTML(b *testing.B) {
	code, err := os.ReadFile("../../internal/engine/engine_gen.go")
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(code)))
	for b.Loop() {
		var out bytes.Buffer
		if status := run([]string{"-l", "go"}, bytes.NewReader(code), &out, &out); status != 0 {
			b.Fatal(out.String())
		}
	}
}
