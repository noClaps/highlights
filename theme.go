package highlights

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
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

// Theme is a Zed-compatible color theme.
type Theme struct {
	Name       string
	Appearance string
	// Style holds colors and syntax scopes, in theme-file key order.
	Style *Object
	// CSSVariables makes colors CSS custom properties. Nil disables it.
	CSSVariables *CSSVariables
}

// CSSVariables configures a CSS-variable theme. With All, every token slot
// uses its prefixed custom property and Style is not read. Otherwise the
// theme is a palette: syntax colors name variable suffixes.
type CSSVariables struct {
	All      bool
	Prefix   *string
	Defaults map[string]string
}

// ThemeFamily is a Zed-compatible collection of themes.
type ThemeFamily struct {
	Name   string
	Author string
	Themes []*Theme
}

// ThemeSource is a *Theme or a *ThemeFamily. A family resolves to its
// first member.
type ThemeSource interface {
	themeSource()
}

func (*Theme) themeSource()       {}
func (*ThemeFamily) themeSource() {}

type themeJSON struct {
	Name         any             `json:"name"`
	Appearance   any             `json:"appearance"`
	Style        *Object         `json:"style"`
	CSSVariables json.RawMessage `json:"cssVariables"`
	Author       any             `json:"author"`
	Themes       []*Theme        `json:"themes"`
}

// UnmarshalJSON decodes a Zed theme object.
func (t *Theme) UnmarshalJSON(data []byte) error {
	var raw themeJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	t.Name, _ = raw.Name.(string)
	t.Appearance, _ = raw.Appearance.(string)
	t.Style = raw.Style
	t.CSSVariables = nil
	switch s := strings.TrimSpace(string(raw.CSSVariables)); {
	case s == "true":
		t.CSSVariables = &CSSVariables{All: true}
	case strings.HasPrefix(s, "{"):
		var cfg struct {
			Prefix   *string           `json:"prefix"`
			Defaults map[string]string `json:"defaults"`
		}
		if err := json.Unmarshal(raw.CSSVariables, &cfg); err != nil {
			return err
		}
		t.CSSVariables = &CSSVariables{Prefix: cfg.Prefix, Defaults: cfg.Defaults}
	}
	return nil
}

// MarshalJSON encodes the theme as a Zed theme object.
func (t *Theme) MarshalJSON() ([]byte, error) {
	out := NewObject("name", t.Name, "appearance", t.Appearance)
	if t.Style != nil {
		out.Set("style", t.Style)
	} else {
		out.Set("style", &Object{})
	}
	if c := t.CSSVariables; c != nil {
		if c.All {
			out.Set("cssVariables", true)
		} else {
			cfg := &Object{}
			if c.Prefix != nil {
				cfg.Set("prefix", *c.Prefix)
			}
			if c.Defaults != nil {
				defaults := &Object{}
				for k, v := range c.Defaults {
					defaults.Set(k, v)
				}
				cfg.Set("defaults", defaults)
			}
			out.Set("cssVariables", cfg)
		}
	}
	return out.MarshalJSON()
}

// ParseTheme decodes a theme or theme family from JSON.
func ParseTheme(data []byte) (ThemeSource, error) {
	var raw themeJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if raw.Themes != nil {
		f := &ThemeFamily{Themes: raw.Themes}
		f.Name, _ = raw.Name.(string)
		f.Author, _ = raw.Author.(string)
		return f, nil
	}
	t := &Theme{}
	if err := t.UnmarshalJSON(data); err != nil {
		return nil, err
	}
	return t, nil
}

// MustParseTheme is ParseTheme for themes known to be valid.
func MustParseTheme(data []byte) *Theme {
	src, err := ParseTheme(data)
	if err != nil {
		panic(err)
	}
	t, err := ResolveTheme(src)
	if err != nil {
		panic(err)
	}
	return t
}

var (
	colorRe     = regexp.MustCompile(`(?i)^#([a-f0-9]{3,4}|[a-f0-9]{6}|[a-f0-9]{8})$`)
	displayP3Re = regexp.MustCompile(`(?i)^color\(display-p3\s+[+-]?(?:\d+(?:\.\d*)?|\.\d+)\s+[+-]?(?:\d+(?:\.\d*)?|\.\d+)\s+[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:\s*/\s*[+-]?(?:\d+(?:\.\d*)?|\.\d+))?\s*\)$`)
)

// TokenStyle is one theme slot's resolved styling for a token type.
type TokenStyle struct {
	// Color is a CSS color, or "" when the slot inherits the foreground.
	Color  string
	Italic bool
	Weight int
}

// IsThemeColor accepts hex and numeric Display P3 colors that cannot escape
// a CSS declaration. Named colors, variables, and other CSS are rejected.
func IsThemeColor(value any) bool {
	s, ok := value.(string)
	if !ok {
		return false
	}
	s = jsTrim(s)
	return colorRe.MatchString(s) || displayP3Re.MatchString(s)
}

// nullish reports JavaScript's null or undefined.
func styleValue(style *Object, key string) any {
	return style.Value(key)
}

func coalesce(values ...any) any {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}

// ThemeForeground returns `editor.foreground`, `text`, then `foreground`.
func ThemeForeground(style *Object) any {
	return coalesce(styleValue(style, "editor.foreground"), styleValue(style, "text"), styleValue(style, "foreground"))
}

// ThemeBackground returns `editor.background`, then `background`.
func ThemeBackground(style *Object) any {
	return coalesce(styleValue(style, "editor.background"), styleValue(style, "background"))
}

// AnsiNames lists ANSI color names in SGR order, as in Zed's
// `terminal.ansi.<name>` keys.
var AnsiNames = []string{
	"black", "red", "green", "yellow", "blue", "magenta", "cyan", "white",
	"bright_black", "bright_red", "bright_green", "bright_yellow",
	"bright_blue", "bright_magenta", "bright_cyan", "bright_white",
}

// VS Code's terminal colors, which Shiki uses when a theme has none.
var ansiDefaults = []string{
	"#000000", "#cd3131", "#0dbc79", "#e5e510", "#2472c8", "#bc3fbc",
	"#11a8cd", "#e5e5e5", "#666666", "#f14c4c", "#23d18b", "#f5f543",
	"#3b8eea", "#d670d6", "#29b8db", "#ffffff",
}

// ThemeAnsiColors returns a theme's 16 ANSI colors. Missing or invalid
// colors use VS Code's.
func ThemeAnsiColors(style *Object) []string {
	out := make([]string, len(AnsiNames))
	for i, name := range AnsiNames {
		color := resolveThemeStyle(NewObject("foreground", styleValue(style, "terminal.ansi."+name)), "foreground").Color
		if color == "" {
			color = ansiDefaults[i]
		}
		out[i] = color
	}
	return out
}

func describeThemeValue(value any) string {
	switch v := value.(type) {
	case nil:
		return "undefined"
	case *Theme:
		if v == nil {
			return "undefined"
		}
		return "an object without a `name`"
	case *ThemeFamily:
		if v == nil {
			return "undefined"
		}
		var first any
		if len(v.Themes) > 0 {
			first = v.Themes[0]
		}
		return "a ThemeFamily whose first member is " + describeThemeValue(first)
	}
	return fmt.Sprintf("a %T", value)
}

// ResolveTheme returns a theme, or the first member of a family.
func ResolveTheme(theme ThemeSource) (*Theme, error) {
	var resolved *Theme
	switch t := theme.(type) {
	case *Theme:
		resolved = t
	case *ThemeFamily:
		if t != nil && len(t.Themes) > 0 {
			resolved = t.Themes[0]
		}
	}
	if resolved == nil || resolved.Name == "" {
		return nil, &TypeError{"invalid theme: expected a Theme or ThemeFamily object, received " + describeThemeValue(theme)}
	}
	return resolved, nil
}

// syntaxSettings are the fields of a resolved syntax scope.
type syntaxSettings struct {
	color, fontStyle, fontWeight any
}

// resolveThemeSyntax keeps the nearest scope's font settings while finding
// an inherited color.
func resolveThemeSyntax(syntax *Object, name string) *syntaxSettings {
	var settings *syntaxSettings
	for k := name; k != ""; {
		v := syntax.Value(k)
		switch x := v.(type) {
		case string:
			out := syntaxSettings{color: x}
			if settings != nil {
				out.fontStyle, out.fontWeight = settings.fontStyle, settings.fontWeight
			}
			return &out
		case *Object:
			if settings == nil {
				settings = &syntaxSettings{x.Value("color"), x.Value("font_style"), x.Value("font_weight")}
			}
			if c := x.Value("color"); c != nil {
				out := *settings
				out.color = c
				return &out
			}
		}
		if dot := strings.LastIndexByte(k, '.'); dot < 0 {
			k = ""
		} else {
			k = k[:dot]
		}
	}
	return settings
}

func syntaxOf(style *Object) *Object {
	s, _ := style.Value("syntax").(*Object)
	return s
}

// resolveThemeStyle resolves colors and font settings shared by HTML and
// token output.
func resolveThemeStyle(style *Object, name string) TokenStyle {
	var color, fontStyle, fontWeight any
	switch name {
	case "background":
		color = ThemeBackground(style)
	case "foreground":
		color = ThemeForeground(style)
	default:
		if s := resolveThemeSyntax(syntaxOf(style), name); s != nil {
			color, fontStyle, fontWeight = s.color, s.fontStyle, s.fontWeight
		}
		if (fontStyle != nil || fontWeight != nil) && color == nil {
			color = ThemeForeground(style)
		}
	}
	out := TokenStyle{}
	if IsThemeColor(color) {
		c := strings.ToLower(jsTrim(color.(string)))
		if strings.HasPrefix(c, "#") {
			if len(c) <= 5 {
				var sb strings.Builder
				for _, r := range c {
					sb.WriteRune(r)
					if r != '#' {
						sb.WriteRune(r)
					}
				}
				c = sb.String()
			}
			if len(c) == 9 && strings.HasSuffix(c, "ff") {
				c = c[:7]
			}
			// A zero RGBA record inherits the foreground in the Wasm emitter.
			if c == "#00000000" {
				c = ""
			}
		}
		out.Color = c
	}
	out.Italic = fontStyle == "italic"
	if fontWeight != nil {
		w := jsNumber(fontWeight)
		if w >= 100 && w <= 900 {
			out.Weight = int(jsRound(w/100) * 100)
		}
	}
	return out
}

func cssVariable(name, prefix string) string {
	return "var(" + prefix + strings.NewReplacer(".", "-", "_", "-").Replace(name) + ")"
}

// EscapeAttribute escapes a string for a double-quoted HTML attribute.
func EscapeAttribute(value string) string {
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
	prefix := DefaultCSSVariablePrefix
	if cfg.Prefix != nil {
		prefix = *cfg.Prefix
	}
	variable := func(name string) string {
		if fallback := cfg.Defaults[name]; fallback != "" {
			return "var(" + prefix + name + ", " + fallback + ")"
		}
		return "var(" + prefix + name + ")"
	}
	styles := make([]*TokenStyle, len(TokenTypes))
	syntax := syntaxOf(theme.Style)
	for i := 1; i < len(TokenTypes); i++ {
		setting := resolveThemeSyntax(syntax, TokenTypes[i])
		color := "foreground"
		s := &TokenStyle{}
		if setting != nil {
			if setting.color != nil {
				color = jsString(setting.color)
			}
			s.Italic = setting.fontStyle == "italic"
			if setting.fontWeight != nil {
				w := jsNumber(setting.fontWeight)
				if !math.IsNaN(w) {
					s.Weight = int(jsRound(w/100) * 100)
				}
			}
		}
		s.Color = variable(color)
		styles[i] = s
	}
	fgName, bgName := "foreground", "background"
	if v := ThemeForeground(theme.Style); v != nil {
		fgName = jsString(v)
	}
	if v := ThemeBackground(theme.Style); v != nil {
		bgName = jsString(v)
	}
	p := &PreparedTheme{Name: theme.Name, Styles: styles, Fg: variable(fgName), Bg: variable(bgName)}
	p.HTMLOpeners = themeHTMLOpeners(styles, p.Fg, p.Bg)
	p.ansiFn = func() []string {
		out := make([]string, len(AnsiNames))
		for i, name := range AnsiNames {
			if s, ok := theme.Style.Value("terminal.ansi." + name).(string); ok {
				out[i] = variable(s)
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
	style := theme.Style
	styles := make([]*TokenStyle, len(TokenTypes))
	table := make([]byte, len(TokenTypes)*5)
	var fg, bg string
	usesDisplayP3 := false
	for i := 1; i < len(TokenTypes); i++ {
		name := TokenTypes[i]
		s := resolveThemeStyle(style, name)
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
	p.ansiFn = func() []string { return ThemeAnsiColors(style) }
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
	openers := []string{`<pre class="highlights" style="` + EscapeAttribute(root) + `"><code>`}
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
		openers = append(openers, `<span style="`+EscapeAttribute(css)+`">`)
	}
	return packHTMLOpeners(openers)
}

var cssNameRe = regexp.MustCompile(`(?i)^[a-z0-9_.-]+$`)

// ToCSS converts a theme to CSS custom-property declarations. It emits only
// hex and numeric Display P3 colors, omits invalid background and
// foreground colors, uses `inherit` for invalid syntax colors, and skips
// syntax scopes whose names are not plain dotted identifiers. The 16
// `terminal-ansi-*` colors use VS Code's terminal colors when missing.
func ToCSS(theme *Theme, cssVariablePrefix string) string {
	if cssVariablePrefix == "" {
		cssVariablePrefix = DefaultCSSVariablePrefix
	}
	style := theme.Style
	if style == nil {
		return ""
	}
	var css strings.Builder
	background := ThemeBackground(style)
	foreground := ThemeForeground(style)
	if IsThemeColor(background) {
		css.WriteString(cssVariablePrefix + "background: " + jsTrim(background.(string)) + ";")
	}
	if IsThemeColor(foreground) {
		css.WriteString(cssVariablePrefix + "foreground: " + jsTrim(foreground.(string)) + ";")
	}
	syntax := syntaxOf(style)
	seen := map[string]bool{}
	var names []string
	for _, name := range syntax.Keys() {
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
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
		var color any
		if s := resolveThemeSyntax(syntax, name); s != nil {
			color = s.color
		}
		if color == nil {
			color = foreground
		}
		value := "inherit"
		if IsThemeColor(color) {
			value = jsTrim(color.(string))
		}
		css.WriteString(cssVariablePrefix + strings.NewReplacer(".", "-", "_", "-").Replace(name) + ": " + value + ";")
	}
	for i, c := range ThemeAnsiColors(style) {
		css.WriteString(cssVariablePrefix + "terminal-ansi-" + strings.Replace(AnsiNames[i], "_", "-", 1) + ": " + c + ";")
	}
	return css.String()
}

// ResolveThemeStyle resolves the color and font settings of a token type,
// such as "keyword.control", or of "foreground" or "background", as the
// highlighter applies them.
func ResolveThemeStyle(style *Object, name string) TokenStyle {
	return resolveThemeStyle(style, name)
}
