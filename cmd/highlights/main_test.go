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
func call(t *testing.T, stdin string, terminal bool, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	status := run(args, strings.NewReader(stdin), &stdout, &stderr, terminal)
	return status, stdout.String(), stderr.String()
}

var escapeRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

func TestHighlightsStandardInput(t *testing.T) {
	code := "def f(x):\n    return x  # hi\n"
	status, out, errOut := call(t, code, false, "-l", "python", "-color", "always")
	if status != 0 {
		t.Fatalf("status %d: %s", status, errOut)
	}
	if got := escapeRe.ReplaceAllString(out, ""); got != code {
		t.Errorf("text without escapes = %q, want the input %q", got, code)
	}
	// pierre-dark colors the keyword and the comment differently
	if !strings.Contains(out, "\x1b[38;2;255;103;141mdef ") || !strings.Contains(out, "\x1b[38;2;115;115;115m# hi\x1b[0m") {
		t.Errorf("missing keyword or comment colors in %q", out)
	}
}

func TestHighlightsAFileByItsName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	status, out, errOut := call(t, "", true, path)
	if status != 0 {
		t.Fatalf("status %d: %s", status, errOut)
	}
	if !strings.HasPrefix(out, "\x1b[38;2;255;103;141mpackage ") {
		t.Errorf("Go keyword not colored: %q", out)
	}
	if !strings.HasSuffix(out, "\x1b[0m\n") {
		t.Errorf("output does not end the line as the input does: %q", out)
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

func TestColorsOnlyATerminalByDefault(t *testing.T) {
	code := "x := 1\n"
	_, out, _ := call(t, code, false, "-l", "go")
	if out != code {
		t.Errorf("piped output = %q, want the input unchanged", out)
	}
	_, out, _ = call(t, code, false, "-l", "go", "-color", "never")
	if out != code {
		t.Errorf("-color never output = %q, want the input unchanged", out)
	}
	t.Setenv("NO_COLOR", "1")
	_, out, _ = call(t, code, true, "-l", "go")
	if out != code {
		t.Errorf("NO_COLOR output = %q, want the input unchanged", out)
	}
}

func TestPaintsTheBackgroundWhenAsked(t *testing.T) {
	_, out, _ := call(t, "x\n", true, "-l", "go", "-bg")
	if !strings.Contains(out, "\x1b[48;2;") {
		t.Errorf("no background escape in %q", out)
	}
}

func TestWritesHTML(t *testing.T) {
	status, out, errOut := call(t, "x := 1", false, "-l", "go", "-f", "html", "-t", "github-light")
	if status != 0 {
		t.Fatalf("status %d: %s", status, errOut)
	}
	if !strings.HasPrefix(out, `<pre class="highlights"`) || !strings.HasSuffix(out, "</code></pre>\n") {
		t.Errorf("not an HTML block: %q", out)
	}
}

func TestReadsAThemeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mine.json")
	theme := `{"name": "Mine", "appearance": "dark", "style": {"editor.foreground": "#123456", "syntax": {"keyword": {"color": "#abcdef", "font_weight": 700}}}}`
	if err := os.WriteFile(path, []byte(theme), 0o644); err != nil {
		t.Fatal(err)
	}
	status, out, errOut := call(t, "return", true, "-l", "js", "-t", path)
	if status != 0 {
		t.Fatalf("status %d: %s", status, errOut)
	}
	if want := "\x1b[38;2;171;205;239m\x1b[1mreturn\x1b[0m"; out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestReportsUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"-l", "nosuchlanguage"},
		{"-t", "nosuchtheme"},
		{"-f", "pdf"},
		{"-color", "sometimes"},
		{"a.go", "b.go"},
		{"-nosuchflag"},
	} {
		status, out, errOut := call(t, "x", true, args...)
		if status != 2 || out != "" || errOut == "" {
			t.Errorf("%v: status %d, stdout %q, stderr %q; want status 2 and a message", args, status, out, errOut)
		}
	}
}

func TestReportsAMissingFile(t *testing.T) {
	status, _, errOut := call(t, "", true, filepath.Join(t.TempDir(), "missing.go"))
	if status != 1 || !strings.Contains(errOut, "missing.go") {
		t.Errorf("status %d, stderr %q; want status 1 naming the file", status, errOut)
	}
}

func TestListsLanguagesAndThemes(t *testing.T) {
	_, out, _ := call(t, "", false, "-list-langs")
	if !regexp.MustCompile(`(?m)^.*\bgo\b.*$`).MatchString(out) || !strings.Contains(out, "python") {
		t.Errorf("languages missing go or python:\n%s", out)
	}
	_, out, _ = call(t, "", false, "-list-themes")
	if !strings.Contains(out, "pierre-dark\n") || !strings.Contains(out, "github-light\n") {
		t.Errorf("themes missing pierre-dark or github-light:\n%s", out)
	}
}

func BenchmarkANSI(b *testing.B) {
	code, err := os.ReadFile("../../internal/engine/engine_gen.go")
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(code)))
	for b.Loop() {
		var out bytes.Buffer
		if status := run([]string{"-l", "go", "-color", "always"}, bytes.NewReader(code), &out, &out, true); status != 0 {
			b.Fatal(out.String())
		}
	}
}
