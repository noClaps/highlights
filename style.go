package highlights

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"regexp"
	"slices"
	"strings"
)

// Theme is a Zed-compatible color theme.
type Theme struct {
	Name       string
	Appearance string // "dark" or "light"
	Style      ThemeStyle
	// CSSVariables makes colors CSS custom properties; nil disables them.
	CSSVariables *CSSVariables
}

// CSSVariables configures a CSS-variable theme. With All, every token slot
// uses its prefixed custom property and the theme's Style is not read.
// Otherwise the theme is a palette whose syntax colors name variables.
type CSSVariables struct {
	All      bool
	Prefix   string // palette variable prefix; "" means DefaultCSSVariablePrefix
	Defaults map[string]string
}

// ThemeFamily is a Zed-compatible collection of themes.
type ThemeFamily struct {
	Name   string
	Author string
	Themes []*Theme
}

// ThemeSource is a *Theme or a *ThemeFamily. A family resolves to its first
// member.
type ThemeSource interface {
	firstTheme() *Theme
}

func (t *Theme) firstTheme() *Theme { return t }

func (f *ThemeFamily) firstTheme() *Theme {
	if f == nil || len(f.Themes) == 0 {
		return nil
	}
	return f.Themes[0]
}

// ThemeStyle is the style section of a Zed theme.
type ThemeStyle struct {
	// Colors maps keys such as "editor.background" and "terminal.ansi.red"
	// to CSS colors.
	Colors map[string]string
	// Players are the collaborator colors.
	Players []Player
	// Syntax lists the syntax scopes in theme-file order, which ToCSS keeps.
	Syntax []SyntaxRule

	// keys whose JSON values were not strings; their Colors entries hold the
	// value as JavaScript would print it, which is never a valid color
	nonString map[string]bool
}

// Player holds the colors of one collaborator slot.
type Player struct {
	Cursor     string `json:"cursor,omitempty"`
	Selection  string `json:"selection,omitempty"`
	Background string `json:"background,omitempty"`
}

// SyntaxRule styles a syntax scope such as "keyword.control". A scope
// without a color inherits the color of its nearest parent scope that has
// one; its font settings still apply.
type SyntaxRule struct {
	Scope      string
	Color      string
	FontStyle  string // "italic" or "normal"
	FontWeight int    // 100 to 900
}

// Rule returns the rule for a scope.
func (s *ThemeStyle) Rule(scope string) (SyntaxRule, bool) {
	for _, r := range s.Syntax {
		if r.Scope == scope {
			return r, true
		}
	}
	return SyntaxRule{}, false
}

// Foreground returns the default text color: "editor.foreground", "text",
// then "foreground".
func (s *ThemeStyle) Foreground() string {
	c, _ := s.firstColor("editor.foreground", "text", "foreground")
	return c
}

// Background returns the editor background: "editor.background", then
// "background".
func (s *ThemeStyle) Background() string {
	c, _ := s.firstColor("editor.background", "background")
	return c
}

// firstColor returns the first key present, as JavaScript's `??` does.
func (s *ThemeStyle) firstColor(keys ...string) (string, bool) {
	for _, k := range keys {
		if c, ok := s.Colors[k]; ok {
			return c, true
		}
	}
	return "", false
}

// AnsiNames lists ANSI color names in SGR order, as in Zed's
// "terminal.ansi.<name>" keys.
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

// AnsiColors returns the 16 ANSI colors; missing or invalid colors use VS
// Code's.
func (s *ThemeStyle) AnsiColors() []string {
	out := make([]string, len(AnsiNames))
	for i, name := range AnsiNames {
		out[i] = normalizeColor(s.Colors["terminal.ansi."+name])
		if out[i] == "" {
			out[i] = ansiDefaults[i]
		}
	}
	return out
}

var (
	colorRe     = regexp.MustCompile(`(?i)^#([a-f0-9]{3,4}|[a-f0-9]{6}|[a-f0-9]{8})$`)
	displayP3Re = regexp.MustCompile(`(?i)^color\(display-p3\s+[+-]?(?:\d+(?:\.\d*)?|\.\d+)\s+[+-]?(?:\d+(?:\.\d*)?|\.\d+)\s+[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:\s*/\s*[+-]?(?:\d+(?:\.\d*)?|\.\d+))?\s*\)$`)
)

// IsThemeColor accepts hex and numeric Display P3 colors, which cannot
// escape a CSS declaration. Named colors, variables, and other CSS are
// rejected.
func IsThemeColor(s string) bool {
	s = jsTrim(s)
	return colorRe.MatchString(s) || displayP3Re.MatchString(s)
}

// normalizeColor returns a valid color in lowercase, with short hex forms
// expanded and an opaque alpha dropped, or "" for an invalid color. A fully
// transparent black also returns "": the emitter inherits for zero records.
func normalizeColor(c string) string {
	if !IsThemeColor(c) {
		return ""
	}
	c = strings.ToLower(jsTrim(c))
	if !strings.HasPrefix(c, "#") {
		return c
	}
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
	if c == "#00000000" {
		return ""
	}
	return c
}

// TokenStyle is a resolved style for one token type.
type TokenStyle struct {
	Color  string // a CSS color; "" inherits the foreground
	Italic bool
	Weight int // 0, or a font weight from 100 to 900
}

// scopeStyle is the nearest scope's font settings with the nearest color.
type scopeStyle struct {
	color      string
	hasColor   bool
	fontStyle  string
	fontWeight int
}

// resolveScope keeps the nearest scope's font settings while finding an
// inherited color: "function.method" falls back to "function".
func resolveScope(rules map[string]*SyntaxRule, name string) (scopeStyle, bool) {
	var settings *SyntaxRule
	for k := name; k != ""; {
		if r := rules[k]; r != nil {
			if settings == nil {
				settings = r
			}
			if r.Color != "" {
				return scopeStyle{r.Color, true, settings.FontStyle, settings.FontWeight}, true
			}
		}
		if dot := strings.LastIndexByte(k, '.'); dot >= 0 {
			k = k[:dot]
		} else {
			k = ""
		}
	}
	if settings == nil {
		return scopeStyle{}, false
	}
	return scopeStyle{fontStyle: settings.FontStyle, fontWeight: settings.FontWeight}, true
}

// rulesByScope indexes the syntax rules; a later duplicate wins.
func (s *ThemeStyle) rulesByScope() map[string]*SyntaxRule {
	m := make(map[string]*SyntaxRule, len(s.Syntax))
	for i := range s.Syntax {
		m[s.Syntax[i].Scope] = &s.Syntax[i]
	}
	return m
}

// Resolve returns the style of a token type, such as "keyword.control", or
// of "foreground" or "background", as the highlighter applies it.
func (s *ThemeStyle) Resolve(name string) TokenStyle {
	return s.resolve(s.rulesByScope(), name)
}

func (s *ThemeStyle) resolve(rules map[string]*SyntaxRule, name string) TokenStyle {
	var out TokenStyle
	var color string
	switch name {
	case "background":
		color = s.Background()
	case "foreground":
		color = s.Foreground()
	default:
		scope, _ := resolveScope(rules, name)
		color = scope.color
		// font settings without a color take the foreground
		if !scope.hasColor && (scope.fontStyle != "" || scope.fontWeight != 0) {
			color = s.Foreground()
		}
		out.Italic = scope.fontStyle == "italic"
		if w := scope.fontWeight; w >= 100 && w <= 900 {
			out.Weight = int(math.Round(float64(w)/100)) * 100
		}
	}
	out.Color = normalizeColor(color)
	return out
}

// ResolveTheme returns a theme, or the first member of a family.
func ResolveTheme(src ThemeSource) (*Theme, error) {
	var t *Theme
	if src != nil {
		t = src.firstTheme()
	}
	if t == nil || t.Name == "" {
		return nil, newError(ErrInvalidTheme, "invalid theme: expected a Theme or ThemeFamily object, received "+describeTheme(src))
	}
	return t, nil
}

func describeTheme(src ThemeSource) string {
	switch v := src.(type) {
	case *Theme:
		if v != nil {
			return "an object without a `name`"
		}
	case *ThemeFamily:
		if v != nil {
			var first ThemeSource
			if len(v.Themes) > 0 && v.Themes[0] != nil {
				first = v.Themes[0]
			}
			return "a ThemeFamily whose first member is " + describeTheme(first)
		}
	}
	return "undefined"
}

// ---- JSON ----

// ParseTheme decodes a Zed theme or theme family.
func ParseTheme(data []byte) (ThemeSource, error) {
	v, err := decodeJSON(data)
	if err != nil {
		return nil, err
	}
	o, ok := v.(*object)
	if !ok {
		return nil, errors.New("highlights: a theme must be a JSON object")
	}
	if members, ok := o.get("themes"); ok && members != nil {
		f := &ThemeFamily{}
		f.Name, _ = o.value("name").(string)
		f.Author, _ = o.value("author").(string)
		list, _ := members.([]any)
		for _, m := range list {
			mo, _ := m.(*object)
			if mo == nil {
				f.Themes = append(f.Themes, nil)
				continue
			}
			f.Themes = append(f.Themes, themeFromObject(mo))
		}
		return f, nil
	}
	return themeFromObject(o), nil
}

// MustParseTheme parses a theme or family and returns its first theme. It
// panics if the JSON is not a valid theme.
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

// UnmarshalJSON decodes a Zed theme object.
func (t *Theme) UnmarshalJSON(data []byte) error {
	v, err := decodeJSON(data)
	if err != nil {
		return err
	}
	o, ok := v.(*object)
	if !ok {
		return errors.New("highlights: a theme must be a JSON object")
	}
	*t = *themeFromObject(o)
	return nil
}

// MarshalJSON encodes the theme as a Zed theme object.
func (t *Theme) MarshalJSON() ([]byte, error) {
	type cssVariables struct {
		Prefix   string            `json:"prefix,omitempty"`
		Defaults map[string]string `json:"defaults,omitempty"`
	}
	out := struct {
		Name         string     `json:"name"`
		Appearance   string     `json:"appearance"`
		Style        ThemeStyle `json:"style"`
		CSSVariables any        `json:"cssVariables,omitempty"`
	}{Name: t.Name, Appearance: t.Appearance, Style: t.Style}
	if c := t.CSSVariables; c != nil {
		if c.All {
			out.CSSVariables = true
		} else {
			out.CSSVariables = cssVariables{c.Prefix, c.Defaults}
		}
	}
	return json.Marshal(out)
}

// UnmarshalJSON decodes a theme's style object.
func (s *ThemeStyle) UnmarshalJSON(data []byte) error {
	v, err := decodeJSON(data)
	if err != nil {
		return err
	}
	o, _ := v.(*object)
	*s = styleFromObject(o)
	return nil
}

// MarshalJSON encodes the style with its syntax scopes in order.
func (s ThemeStyle) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range slices.Sorted(maps.Keys(s.Colors)) {
		if i > 0 {
			b.WriteByte(',')
		}
		key, _ := json.Marshal(k)
		val, _ := json.Marshal(s.Colors[k])
		b.Write(key)
		b.WriteByte(':')
		b.Write(val)
	}
	if len(s.Players) > 0 {
		if len(s.Colors) > 0 {
			b.WriteByte(',')
		}
		players, err := json.Marshal(s.Players)
		if err != nil {
			return nil, err
		}
		b.WriteString(`"players":`)
		b.Write(players)
	}
	if len(s.Colors) > 0 || len(s.Players) > 0 {
		b.WriteByte(',')
	}
	b.WriteString(`"syntax":{`)
	for i, r := range s.Syntax {
		if i > 0 {
			b.WriteByte(',')
		}
		key, _ := json.Marshal(r.Scope)
		b.Write(key)
		b.WriteByte(':')
		if r.FontStyle == "" && r.FontWeight == 0 {
			val, _ := json.Marshal(r.Color)
			b.Write(val)
			continue
		}
		val, err := json.Marshal(struct {
			Color      string `json:"color,omitempty"`
			FontStyle  string `json:"font_style,omitempty"`
			FontWeight int    `json:"font_weight,omitempty"`
		}{r.Color, r.FontStyle, r.FontWeight})
		if err != nil {
			return nil, err
		}
		b.Write(val)
	}
	b.WriteString("}}")
	return b.Bytes(), nil
}

func themeFromObject(o *object) *Theme {
	t := &Theme{}
	t.Name, _ = o.value("name").(string)
	t.Appearance, _ = o.value("appearance").(string)
	style, _ := o.value("style").(*object)
	t.Style = styleFromObject(style)
	switch c := o.value("cssVariables").(type) {
	case bool:
		if c {
			t.CSSVariables = &CSSVariables{All: true}
		}
	case *object:
		cfg := &CSSVariables{}
		cfg.Prefix, _ = c.value("prefix").(string)
		if d, ok := c.value("defaults").(*object); ok {
			cfg.Defaults = map[string]string{}
			for _, k := range d.keys {
				if s, ok := d.value(k).(string); ok {
					cfg.Defaults[k] = s
				}
			}
		}
		t.CSSVariables = cfg
	}
	return t
}

// styleFromObject reads a decoded style object. A non-string value where a
// color belongs keeps its JavaScript string form: it is present, so it
// stops the foreground and background fallbacks, and it is never valid.
func styleFromObject(o *object) ThemeStyle {
	s := ThemeStyle{Colors: map[string]string{}}
	if o == nil {
		return s
	}
	for _, k := range o.keys {
		v := o.values[k]
		switch k {
		case "syntax":
			syntax, _ := v.(*object)
			if syntax == nil {
				continue
			}
			for _, scope := range syntax.keys {
				s.Syntax = append(s.Syntax, ruleFromValue(scope, syntax.values[scope]))
			}
			continue
		case "players":
			list, _ := v.([]any)
			for _, p := range list {
				po, _ := p.(*object)
				if po == nil {
					continue
				}
				var pl Player
				pl.Cursor, _ = po.value("cursor").(string)
				pl.Selection, _ = po.value("selection").(string)
				pl.Background, _ = po.value("background").(string)
				s.Players = append(s.Players, pl)
			}
			continue
		}
		switch x := v.(type) {
		case nil:
		case string:
			s.Colors[k] = x
		default:
			s.Colors[k] = jsString(x)
			if s.nonString == nil {
				s.nonString = map[string]bool{}
			}
			s.nonString[k] = true
		}
	}
	return s
}

func ruleFromValue(scope string, v any) SyntaxRule {
	r := SyntaxRule{Scope: scope}
	switch x := v.(type) {
	case string:
		r.Color = x
	case *object:
		if c := x.value("color"); c != nil {
			r.Color = jsString(c)
		}
		if fs := x.value("font_style"); fs != nil {
			r.FontStyle = jsString(fs)
		}
		if fw := x.value("font_weight"); fw != nil {
			if w := jsNumber(fw); !math.IsNaN(w) && !math.IsInf(w, 0) {
				r.FontWeight = int(math.Round(w))
			}
		}
	}
	return r
}

func decodeJSON(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := decodeOrdered(dec)
	if err != nil {
		return nil, fmt.Errorf("highlights: %w", err)
	}
	return v, nil
}
