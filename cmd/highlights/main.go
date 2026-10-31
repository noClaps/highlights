// Command highlights writes a source file as syntax-highlighted HTML.
//
// Usage:
//
//	highlights [flags] [file]
//
// It reads the file, or standard input when the file is omitted or "-",
// and writes a <pre> block with inline styles to standard output, as
// CodeToHTML does. The language comes from the file name unless -l names
// it.
//
// Flags:
//
//	-l lang      language name or alias (default: from the file name, else plain text)
//	-t theme     bundled theme name or path to a theme JSON file (default pierre-dark)
//	-list-langs  print the accepted language names and exit
//	-list-themes print the bundled theme names and exit
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"highlights"
	"highlights/themes"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// usageError is an error in how the command was called.
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

// run is the command with its streams passed in; it returns the exit
// status: 0 on success, 1 when reading or highlighting fails, 2 for a
// usage error.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("highlights", flag.ContinueOnError)
	flags.SetOutput(stderr)
	lang := flags.String("l", "", "language name or alias (default: from the file name, else plain text)")
	theme := flags.String("t", "pierre-dark", "bundled theme name or path to a theme JSON file")
	listLangs := flags.Bool("list-langs", false, "print the accepted language names and exit")
	listThemes := flags.Bool("list-themes", false, "print the bundled theme names and exit")
	flags.Usage = func() {
		fmt.Fprintf(stderr, "usage: highlights [flags] [file]\n\nWrites a file, or standard input when the file is omitted or \"-\", as highlighted HTML.\n\n")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	out := bufio.NewWriter(stdout)
	defer out.Flush()
	switch {
	case *listLangs:
		printLanguages(out)
		return 0
	case *listThemes:
		for _, name := range themes.Names() {
			fmt.Fprintln(out, name)
		}
		return 0
	}

	err := highlight(out, stdin, flags.Args(), *lang, *theme)
	if err == nil {
		return 0
	}
	out.Flush()
	fmt.Fprintln(stderr, "highlights:", err)
	var usage usageError
	if errors.As(err, &usage) {
		return 2
	}
	return 1
}

func highlight(out io.Writer, stdin io.Reader, args []string, lang, themeName string) error {
	if len(args) > 1 {
		return usageError{"at most one file may be given"}
	}
	path := "-"
	if len(args) == 1 {
		path = args[0]
	}
	var code []byte
	var err error
	if path == "-" {
		code, err = io.ReadAll(stdin)
	} else {
		code, err = os.ReadFile(path)
	}
	if err != nil {
		return err
	}

	if lang == "" {
		lang = detectLanguage(path)
	} else if !highlights.IsSupportedLanguage(lang) {
		return usageError{fmt.Sprintf("unknown language %q (see -list-langs)", lang)}
	}
	theme, err := loadTheme(themeName)
	if err != nil {
		return err
	}
	html, err := highlights.CodeToHTMLBytes(code, highlights.Options{
		Lang:         lang,
		ThemeOptions: highlights.ThemeOptions{Theme: theme},
	})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "%s\n", html)
	return err
}

// detectLanguage picks a language from a file name: the whole name, as for
// Dockerfile or Makefile, then each extension from the outermost, as for
// main.go or app.test.tsx. Standard input and unknown names are plain
// text.
func detectLanguage(path string) string {
	if path == "-" {
		return "text"
	}
	name := strings.ToLower(filepath.Base(path))
	if highlights.IsSupportedLanguage(name) {
		return name
	}
	for ext := filepath.Ext(name); ext != ""; ext = filepath.Ext(name) {
		if lang := ext[1:]; highlights.IsSupportedLanguage(lang) {
			return lang
		}
		name = strings.TrimSuffix(name, ext)
	}
	return "text"
}

// loadTheme reads a bundled theme by name, or a theme JSON file by path.
func loadTheme(name string) (highlights.ThemeSource, error) {
	data, err := themes.JSON(name)
	if err != nil {
		if !strings.ContainsRune(name, os.PathSeparator) && !strings.HasSuffix(name, ".json") {
			return nil, usageError{fmt.Sprintf("unknown theme %q (see -list-themes)", name)}
		}
		if data, err = os.ReadFile(name); err != nil {
			return nil, err
		}
	}
	return highlights.ParseTheme(data)
}

// printLanguages lists each language with its aliases on one line.
func printLanguages(out io.Writer) {
	byID := map[int][]string{}
	for name, id := range highlights.Languages() {
		byID[id] = append(byID[id], name)
	}
	lines := make([]string, 0, len(byID))
	for _, names := range byID {
		sort.Strings(names)
		lines = append(lines, strings.Join(names, " "))
	}
	sort.Strings(lines)
	for _, l := range lines {
		fmt.Fprintln(out, l)
	}
}
