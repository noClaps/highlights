// Package highlights is a syntax highlighter with 151 built-in languages
// and Zed-compatible themes. It is a Go port of @pierre/highlights.
//
// CodeToHTML renders a <pre class="highlights"> fragment; CodeToTokens
// returns Shiki-compatible themed tokens, one slice per line:
//
//	html, err := highlights.CodeToHTML(code, highlights.Options{
//		Lang:         "go",
//		ThemeOptions: highlights.ThemeOptions{Theme: themes.PierreDark},
//	})
//
// The package-level functions are safe for concurrent use. A Highlighter
// owns one engine instance for repeated calls from one goroutine.
//
// StreamTokenizer tokenizes input that arrives in chunks, and
// LiveTokenizer keeps an editable document whose tokens update
// incrementally after each edit.
//
// # Themes
//
// A theme is a *Theme or a *ThemeFamily, whose first member applies. The
// themes package bundles 75 themes. Set ThemeOptions.Themes instead of
// Theme to render several themes at once with CSS custom properties, or
// use a theme whose CSSVariables is set to emit variables only.
//
// # Offsets
//
// Token offsets and live-document positions count UTF-16 code units, as
// Shiki and the Language Server Protocol do. Input text that is not valid
// UTF-8 is decoded as the WHATWG decoder does; a lone UTF-16 surrogate
// encoded as WTF-8 stays in token content and lexes as U+FFFD.
//
// # Errors
//
// Errors wrap one of ErrUnknownLanguage, ErrInvalidTheme,
// ErrInvalidArgument, or ErrOutOfRange; closed tokenizers return
// ErrStreamEnded or ErrClosed.
package highlights
