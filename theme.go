package highlights

import (
	"encoding/binary"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"weak"
)

// DefaultCSSVariablePrefix prefixes CSS-variable colors and per-theme
// properties unless an option sets another prefix.
const DefaultCSSVariablePrefix = "--hls-"

// themeTableBytes is the size of one packed Wasm theme table: 73 five-byte
// records padded for the emitter's SIMD comparisons.
const themeTableBytes = 384

var cssNameReplacer = strings.NewReplacer(".", "-", "_", "-")

// cssName turns a token type into a custom-property suffix.
func cssName(name string) string { return cssNameReplacer.Replace(name) }

func cssVariable(name, prefix string) string {
	return "var(" + prefix + cssName(name) + ")"
}

// escapeAttribute escapes a string for a double-quoted HTML attribute.
func escapeAttribute(value string) string {
	return attrEscaper.Replace(value)
}

var attrEscaper = strings.NewReplacer("&", "&amp;", `"`, "&quot;", "<", "&lt;", ">", "&gt;")

// packHTMLOpeners packs HTML openers for Wasm mode 4: the u32 span reserve,
// u32 offset/length pairs per token ID, then the UTF-8 bytes.
func packHTMLOpeners(openers []string) []byte {
	offset := 4 + 8*len(openers)
	total := offset
	for _, o := range openers {
		total += len(o)
	}
	blob := make([]byte, total)
	spanReserve := 32
	for i, o := range openers {
		binary.LittleEndian.PutUint32(blob[4+8*i:], uint32(offset))
		binary.LittleEndian.PutUint32(blob[8+8*i:], uint32(len(o)))
		copy(blob[offset:], o)
		offset += len(o)
		if i != 0 {
			spanReserve = max(spanReserve, len(o)+32)
		}
	}
	binary.LittleEndian.PutUint32(blob, uint32(spanReserve))
	return blob
}

// PreparedTheme holds resolved token styles, root colors, and the theme's
// HTML representation.
type PreparedTheme struct {
	Name string
	// Styles is indexed by token ID; nil slots inherit the foreground.
	Styles []*TokenStyle
	// Fg and Bg are "" when the theme has none.
	Fg, Bg string
	// Table holds five-byte RGBA/style records; nil for Display P3 and CSS
	// variables.
	Table []byte
	// HTMLOpeners holds UTF-8 HTML openers for Display P3 themes and named
	// CSS palettes.
	HTMLOpeners []byte

	ansiOnce sync.Once
	ansiFn   func() []string
	ansi     []string
}

// Ansi returns colors for ANSI palette indexes 0-15, in AnsiNames order.
func (p *PreparedTheme) Ansi() []string {
	p.ansiOnce.Do(func() { p.ansi = p.ansiFn() })
	return p.ansi
}

type themeCacheKey struct {
	theme  weak.Pointer[Theme]
	prefix string
}

var (
	themeCacheMu sync.Mutex
	themeCache   = map[themeCacheKey]*PreparedTheme{}
	themeTracked = map[weak.Pointer[Theme]]bool{}
)

// PrepareTheme resolves all token styles for a theme or family. Results are
// cached by theme pointer and, for CSS-variable themes, by prefix; replace
// the theme object to change its cached styles.
func PrepareTheme(theme ThemeSource, cssVariablePrefix string) (*PreparedTheme, error) {
	resolved, err := ResolveTheme(theme)
	if err != nil {
		return nil, err
	}
	if cssVariablePrefix == "" {
		cssVariablePrefix = DefaultCSSVariablePrefix
	}
	all := resolved.CSSVariables != nil && resolved.CSSVariables.All
	prefix := ""
	if all {
		prefix = cssVariablePrefix
	}
	key := themeCacheKey{weak.Make(resolved), prefix}
	themeCacheMu.Lock()
	cached := themeCache[key]
	themeCacheMu.Unlock()
	if cached != nil {
		return cached, nil
	}
	var prepared *PreparedTheme
	switch {
	case all:
		prepared = prepareCSSVariables(resolved.Name, cssVariablePrefix)
	case resolved.CSSVariables != nil:
		prepared = prepareCSSPalette(resolved)
	default:
		prepared = prepareStyles(resolved)
	}
	themeCacheMu.Lock()
	defer themeCacheMu.Unlock()
	if cached := themeCache[key]; cached != nil {
		return cached, nil
	}
	themeCache[key] = prepared
	if !themeTracked[key.theme] {
		themeTracked[key.theme] = true
		runtime.AddCleanup(resolved, func(p weak.Pointer[Theme]) {
			themeCacheMu.Lock()
			defer themeCacheMu.Unlock()
			delete(themeTracked, p)
			for k, prepared := range themeCache {
				if k.theme == p {
					delete(themeCache, k)
					forgetPrepared(prepared)
				}
			}
		}, key.theme)
	}
	return prepared, nil
}

func prepareCSSPalette(theme *Theme) *PreparedTheme {
	cfg := theme.CSSVariables
	prefix := cfg.Prefix
	if prefix == "" {
		prefix = DefaultCSSVariablePrefix
	}
	variable := func(name string) string {
		if fallback := cfg.Defaults[name]; fallback != "" {
			return "var(" + prefix + name + ", " + fallback + ")"
		}
		return "var(" + prefix + name + ")"
	}
	style := &theme.Style
	rules := style.rulesByScope()
	styles := make([]*TokenStyle, len(TokenTypes))
	for i := 1; i < len(TokenTypes); i++ {
		scope, _ := resolveScope(rules, TokenTypes[i])
		color := "foreground"
		if scope.hasColor {
			color = scope.color
		}
		styles[i] = &TokenStyle{
			Color:  variable(color),
			Italic: scope.fontStyle == "italic",
			Weight: int(jsRound(float64(scope.fontWeight)/100)) * 100,
		}
	}
	fgName, bgName := "foreground", "background"
	if c, ok := style.firstColor("editor.foreground", "text", "foreground"); ok {
		fgName = c
	}
	if c, ok := style.firstColor("editor.background", "background"); ok {
		bgName = c
	}
	p := &PreparedTheme{Name: theme.Name, Styles: styles, Fg: variable(fgName), Bg: variable(bgName)}
	p.HTMLOpeners = themeHTMLOpeners(styles, p.Fg, p.Bg)
	p.ansiFn = func() []string {
		out := make([]string, len(AnsiNames))
		for i, name := range AnsiNames {
			key := "terminal.ansi." + name
			if c, ok := style.Colors[key]; ok && !style.nonString[key] {
				out[i] = variable(c)
			} else {
				out[i] = variable("terminal-ansi-" + strings.Replace(name, "_", "-", 1))
			}
		}
		return out
	}
	return p
}

func prepareCSSVariables(name, prefix string) *PreparedTheme {
	styles := make([]*TokenStyle, len(TokenTypes))
	for i := 1; i < len(TokenTypes); i++ {
		styles[i] = &TokenStyle{Color: cssVariable(TokenTypes[i], prefix)}
	}
	p := &PreparedTheme{
		Name:   name,
		Styles: styles,
		Fg:     cssVariable("foreground", prefix),
		Bg:     cssVariable("background", prefix),
	}
	p.ansiFn = func() []string {
		out := make([]string, len(AnsiNames))
		for i, n := range AnsiNames {
			out[i] = cssVariable("terminal.ansi."+n, prefix)
		}
		return out
	}
	return p
}

func prepareStyles(theme *Theme) *PreparedTheme {
	style := &theme.Style
	rules := style.rulesByScope()
	styles := make([]*TokenStyle, len(TokenTypes))
	table := make([]byte, len(TokenTypes)*5)
	var fg, bg string
	usesDisplayP3 := false
	for i := 1; i < len(TokenTypes); i++ {
		name := TokenTypes[i]
		s := style.resolve(rules, name)
		o := i * 5
		if s.Color != "" {
			if strings.HasPrefix(s.Color, "#") {
				rgb, _ := strconv.ParseUint(s.Color[1:7], 16, 32)
				table[o] = byte(rgb >> 16)
				table[o+1] = byte(rgb >> 8)
				table[o+2] = byte(rgb)
				table[o+3] = 0xff
				if len(s.Color) == 9 {
					a, _ := strconv.ParseUint(s.Color[7:], 16, 8)
					table[o+3] = byte(a)
				}
			} else {
				usesDisplayP3 = true
			}
		}
		italic := byte(0)
		if s.Italic {
			italic = 0x10
		}
		table[o+4] = italic | byte(s.Weight/100)
		if s.Color == "" && !s.Italic && s.Weight == 0 {
			continue
		}
		switch name {
		case "foreground":
			fg = s.Color
		case "background":
			bg = s.Color
		default:
			copied := s
			styles[i] = &copied
		}
	}
	p := &PreparedTheme{Name: theme.Name, Styles: styles, Fg: fg, Bg: bg, Table: table}
	if usesDisplayP3 {
		p.Table = nil
		p.HTMLOpeners = themeHTMLOpeners(styles, fg, bg)
	}
	p.ansiFn = style.AnsiColors
	return p
}

func themeHTMLOpeners(styles []*TokenStyle, fg, bg string) []byte {
	root := ""
	if bg != "" {
		root += "background-color:" + bg + ";"
	}
	if fg != "" {
		root += "color:" + fg
	}
	openers := []string{`<pre class="highlights" style="` + escapeAttribute(root) + `"><code>`}
	for i := 1; i < len(TokenTypes); i++ {
		s := styles[i]
		color := "inherit"
		if s != nil && s.Color != "" {
			color = s.Color
		}
		css := "color:" + color
		if s != nil && s.Italic {
			css += ";font-style:italic"
		}
		if s != nil && s.Weight != 0 {
			css += ";font-weight:" + strconv.Itoa(s.Weight)
		}
		openers = append(openers, `<span style="`+escapeAttribute(css)+`">`)
	}
	return packHTMLOpeners(openers)
}

var cssNameRe = regexp.MustCompile(`(?i)^[a-z0-9_.-]+$`)

// ToCSS converts a theme to CSS custom-property declarations. It emits only
// hex and numeric Display P3 colors, omits invalid background and
// foreground colors, uses `inherit` for invalid syntax colors, and skips
// syntax scopes whose names are not plain dotted identifiers. The 16
// `terminal-ansi-*` colors use VS Code's terminal colors when missing. A
// cssVariablePrefix of "" means DefaultCSSVariablePrefix.
func ToCSS(theme *Theme, cssVariablePrefix string) string {
	if cssVariablePrefix == "" {
		cssVariablePrefix = DefaultCSSVariablePrefix
	}
	style := &theme.Style
	var css strings.Builder
	background, foreground := style.Background(), style.Foreground()
	if IsThemeColor(background) {
		css.WriteString(cssVariablePrefix + "background: " + jsTrim(background) + ";")
	}
	if IsThemeColor(foreground) {
		css.WriteString(cssVariablePrefix + "foreground: " + jsTrim(foreground) + ";")
	}
	rules := style.rulesByScope()
	seen := map[string]bool{}
	var names []string
	for _, r := range style.Syntax {
		if !seen[r.Scope] {
			seen[r.Scope] = true
			names = append(names, r.Scope)
		}
	}
	for _, name := range TokenTypes[1 : len(TokenTypes)-2] {
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	for _, name := range names {
		if !cssNameRe.MatchString(name) {
			continue
		}
		color := foreground
		if scope, _ := resolveScope(rules, name); scope.hasColor {
			color = scope.color
		}
		value := "inherit"
		if IsThemeColor(color) {
			value = jsTrim(color)
		}
		css.WriteString(cssVariablePrefix + cssName(name) + ": " + value + ";")
	}
	for i, c := range style.AnsiColors() {
		css.WriteString(cssVariablePrefix + "terminal-ansi-" + strings.Replace(AnsiNames[i], "_", "-", 1) + ": " + c + ";")
	}
	return css.String()
}
