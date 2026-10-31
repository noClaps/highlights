// Command highlights prints a source file with syntax highlighting.
//
// Usage:
//
//	highlights [flags] [file]
//
// It reads the file, or standard input when the file is omitted or "-",
// and writes the code to standard output, colored for a terminal or as
// HTML. The language comes from the file name unless -l names it.
//
// Flags:
//
//	-l lang      language name or alias (default: from the file name, else plain text)
//	-t theme     bundled theme name or path to a theme JSON file (default pierre-dark)
//	-f format    ansi or html (default ansi)
//	-color when  for ansi: auto, always or never (default auto: only on a
//	             terminal, and never when NO_COLOR is set)
//	-bg          for ansi: paint the theme's background color too
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
	"strconv"
	"strings"

	"highlights"
	"highlights/themes"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, isTerminal(os.Stdout)))
}

// usageError is an error in how the command was called.
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

// run is the command with its streams passed in; it returns the exit
// status: 0 on success, 1 when reading or highlighting fails, 2 for a
// usage error.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer, terminal bool) int {
	flags := flag.NewFlagSet("highlights", flag.ContinueOnError)
	flags.SetOutput(stderr)
	lang := flags.String("l", "", "language name or alias (default: from the file name, else plain text)")
	theme := flags.String("t", "pierre-dark", "bundled theme name or path to a theme JSON file")
	format := flags.String("f", "ansi", "output format: ansi or html")
	color := flags.String("color", "auto", "for ansi: auto, always or never")
	background := flags.Bool("bg", false, "for ansi: paint the theme's background color")
	listLangs := flags.Bool("list-langs", false, "print the accepted language names and exit")
	listThemes := flags.Bool("list-themes", false, "print the bundled theme names and exit")
	flags.Usage = func() {
		fmt.Fprintf(stderr, "usage: highlights [flags] [file]\n\nHighlights a file, or standard input when the file is omitted or \"-\".\n\n")
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

	err := highlight(out, stdin, flags.Args(), options{
		lang:       *lang,
		theme:      *theme,
		format:     *format,
		color:      *color,
		background: *background,
		terminal:   terminal,
	})
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

type options struct {
	lang, theme, format, color string
	background, terminal       bool
}

func highlight(out io.Writer, stdin io.Reader, args []string, o options) error {
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

	lang := o.lang
	if lang == "" {
		lang = detectLanguage(path)
	} else if !highlights.IsSupportedLanguage(lang) {
		return usageError{fmt.Sprintf("unknown language %q (see -list-langs)", lang)}
	}
	theme, err := loadTheme(o.theme)
	if err != nil {
		return err
	}
	opts := highlights.Options{Lang: lang, ThemeOptions: highlights.ThemeOptions{Theme: theme}}

	switch o.format {
	case "html":
		html, err := highlights.CodeToHTMLBytes(code, opts)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "%s\n", html)
		return err
	case "ansi":
	default:
		return usageError{fmt.Sprintf("unknown format %q: want ansi or html", o.format)}
	}

	colored, err := useColor(o.color, o.terminal)
	if err != nil {
		return err
	}
	if !colored {
		_, err := out.Write(code)
		return err
	}
	res, err := highlights.CodeToTokensBytes(code, opts)
	if err != nil {
		return err
	}
	return writeANSI(out, res, o.background)
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

// useColor decides whether ANSI output is colored.
func useColor(when string, terminal bool) (bool, error) {
	switch when {
	case "always":
		return true, nil
	case "never":
		return false, nil
	case "auto":
		_, noColor := os.LookupEnv("NO_COLOR")
		return terminal && !noColor, nil
	}
	return false, usageError{fmt.Sprintf("unknown -color %q: want auto, always or never", when)}
}

// writeANSI writes the tokens with 24-bit color escapes. A style is written
// only where it changes, and reset at the end of each line so that a line
// never bleeds into the next.
func writeANSI(out io.Writer, res *highlights.TokensResult, background bool) error {
	w := bufio.NewWriter(out)
	prefix := ""
	if background {
		prefix = sgrColor(48, res.Bg)
	}
	// a theme has few styles, so each escape is built once
	type style struct {
		color string
		font  highlights.FontStyle
	}
	escapes := map[style]string{}
	for i, line := range res.Tokens {
		if i > 0 {
			w.WriteByte('\n')
		}
		current := ""
		for _, t := range line {
			s := style{t.Color, t.FontStyle}
			if s.color == "" {
				s.color = res.Fg
			}
			sgr, ok := escapes[s]
			if !ok {
				sgr = prefix + sgrColor(38, s.color) + sgrFont(s.font)
				escapes[s] = sgr
			}
			if sgr != current {
				if current != "" {
					w.WriteString("\x1b[0m")
				}
				w.WriteString(sgr)
				current = sgr
			}
			w.WriteString(t.Content)
		}
		if current != "" {
			w.WriteString("\x1b[0m")
		}
	}
	return w.Flush()
}

// sgrColor writes the escape setting the foreground (38) or background
// (48) to a #rgb, #rrggbb or #rrggbbaa color, or "" for any other value.
func sgrColor(target int, color string) string {
	hex := strings.TrimPrefix(color, "#")
	if len(hex) == len(color) {
		return ""
	}
	switch len(hex) {
	case 3:
		hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
	case 6:
	case 8:
		hex = hex[:6]
	default:
		return ""
	}
	rgb, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("\x1b[%d;2;%d;%d;%dm", target, rgb>>16, rgb>>8&0xff, rgb&0xff)
}

// sgrFont writes the escapes for a token's font style.
func sgrFont(style highlights.FontStyle) string {
	var codes []string
	for _, f := range []struct {
		flag highlights.FontStyle
		code string
	}{
		{highlights.FontBold, "1"},
		{highlights.FontItalic, "3"},
		{highlights.FontUnderline, "4"},
		{highlights.FontStrikethrough, "9"},
	} {
		if style&f.flag != 0 {
			codes = append(codes, f.code)
		}
	}
	if len(codes) == 0 {
		return ""
	}
	return "\x1b[" + strings.Join(codes, ";") + "m"
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

// isTerminal reports whether f is a character device, such as a terminal.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
