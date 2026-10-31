package highlights

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// StyleProperty is one CSS declaration of an HTMLStyle.
type StyleProperty struct {
	Name, Value string
}

// HTMLStyle is an ordered list of CSS declarations for a token's span.
type HTMLStyle []StyleProperty

// Get returns a property's value.
func (s HTMLStyle) Get(name string) (string, bool) {
	for _, p := range s {
		if p.Name == name {
			return p.Value, true
		}
	}
	return "", false
}

func (s *HTMLStyle) set(name, value string) {
	for i, p := range *s {
		if p.Name == name {
			(*s)[i].Value = value
			return
		}
	}
	*s = append(*s, StyleProperty{name, value})
}

// CSS renders the declarations as `name:value;...`.
func (s HTMLStyle) CSS() string {
	var sb strings.Builder
	for i, p := range s {
		if i > 0 {
			sb.WriteByte(';')
		}
		sb.WriteString(p.Name)
		sb.WriteByte(':')
		sb.WriteString(p.Value)
	}
	return sb.String()
}

// MarshalJSON encodes the declarations as an object in order.
func (s HTMLStyle) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, p := range s {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(p.Name)
		v, _ := json.Marshal(p.Value)
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// FontStyle holds Shiki's font style flags.
type FontStyle int

// Font style flags.
const (
	FontItalic FontStyle = 1 << iota
	FontBold
	FontUnderline
	FontStrikethrough
)

// TokenType is a standard token type, matching TextMate's
// StandardTokenType. Bracket matching ignores brackets in comments,
// strings, and regular expressions.
type TokenType int

// Standard token types.
const (
	TokenTypeOther TokenType = iota
	TokenTypeComment
	TokenTypeString
	TokenTypeRegex
)

// ThemedToken is a Shiki-compatible styled run within one line. Offsets use
// UTF-16 code units: CodeToTokens and streams use input offsets, and live
// documents use line offsets.
type ThemedToken struct {
	Content string
	Offset  int
	// Color is a theme color or a prefixed `var(...)`; "" when unset.
	Color string
	// BgColor is set only for `ansi`.
	BgColor string
	// FontStyle applies when HTMLStyle is nil.
	FontStyle FontStyle
	// HTMLStyle holds styles for multiple themes; nil for a single theme.
	HTMLStyle HTMLStyle
	Type      TokenType
}

// MarshalJSON encodes the token with Shiki's field names.
func (t ThemedToken) MarshalJSON() ([]byte, error) {
	type token struct {
		Content   string     `json:"content"`
		Offset    int        `json:"offset"`
		Color     string     `json:"color,omitempty"`
		BgColor   string     `json:"bgColor,omitempty"`
		FontStyle *FontStyle `json:"fontStyle,omitempty"`
		HTMLStyle HTMLStyle  `json:"htmlStyle,omitempty"`
		Type      TokenType  `json:"type,omitempty"`
	}
	out := token{t.Content, t.Offset, t.Color, t.BgColor, nil, t.HTMLStyle, t.Type}
	if t.HTMLStyle == nil {
		out.FontStyle = &t.FontStyle
	}
	return json.Marshal(out)
}

// TokensResult is the result of CodeToTokens, matching Shiki's.
type TokensResult struct {
	Tokens    [][]ThemedToken `json:"tokens"`
	Fg        string          `json:"fg,omitempty"`
	Bg        string          `json:"bg,omitempty"`
	ThemeName string          `json:"themeName,omitempty"`
	// RootStyle is set only when no theme applies inline.
	RootStyle string `json:"rootStyle,omitempty"`
}

type themeMeta struct {
	fg, bg, themeName, rootStyle string
}

type themeRole int

const (
	roleSingle themeRole = iota
	roleDefault
	roleVariable
	roleLightDark
)

// resolvedTheme is a prepared theme tagged with its option key ("" for
// `theme`) and role.
type resolvedTheme struct {
	*PreparedTheme
	color string
	role  themeRole
}

// themeSet is a resolved theme list with its rendering caches.
type themeSet struct {
	themes []resolvedTheme

	mu         sync.Mutex
	htmlStyles map[string][]HTMLStyle
	blobs      map[string][]byte
	noBlob     map[string]bool
	openers    map[string][]byte
	metas      map[string]themeMeta
}

// standardTypes maps each token slot to Shiki's ThemedToken.type.
var standardTypes = func() []TokenType {
	out := make([]TokenType, len(TokenTypes))
	for i, name := range TokenTypes {
		switch {
		case name == "string.regex":
			out[i] = TokenTypeRegex
		case name == "comment" || strings.HasPrefix(name, "comment."):
			out[i] = TokenTypeComment
		case name == "string" || strings.HasPrefix(name, "string."):
			out[i] = TokenTypeString
		}
	}
	return out
}()

// ThemeOptions selects one theme or a named set of themes.
type ThemeOptions struct {
	// Theme applies one theme. Set exactly one of Theme and Themes.
	Theme ThemeSource
	// Themes maps color-scheme keys such as "light" and "dark" to themes,
	// rendered with CSS custom properties.
	Themes map[string]ThemeSource
	// DefaultColor is the Themes key applied inline; "" means "light".
	// "light-dark()" combines the light and dark colors.
	DefaultColor string
	// NoDefaultColor uses custom properties for every theme.
	NoDefaultColor bool
	// CSSVariablePrefix prefixes CSS-variable colors and per-theme
	// properties; "" means DefaultCSSVariablePrefix.
	CSSVariablePrefix string
}

func (o *ThemeOptions) prefix() string {
	if o.CSSVariablePrefix == "" {
		return DefaultCSSVariablePrefix
	}
	return o.CSSVariablePrefix
}

// Theme sets are cached per first prepared theme, at most 128 each, so
// their style caches and blob IDs stay stable.
type setList struct {
	keys []string
	sets map[string]*themeSet
}

var (
	setCacheMu  sync.Mutex
	setCache    = map[*PreparedTheme]*setList{}
	preparedIDs = map[*PreparedTheme]int{}
	nextBlobID  atomic.Uint32
)

func preparedID(p *PreparedTheme) int {
	id, ok := preparedIDs[p]
	if !ok {
		id = len(preparedIDs)
		preparedIDs[p] = id
	}
	return id
}

// resolveOptionThemes converts options to a theme list. With Themes, the
// inline DefaultColor theme goes first and the rest sort by key.
func resolveOptionThemes(o *ThemeOptions) (*themeSet, error) {
	prefix := o.prefix()
	if o.Themes == nil {
		p, err := PrepareTheme(o.Theme, prefix)
		if err != nil {
			return nil, err
		}
		return cachedSet([]resolvedTheme{{p, "", roleSingle}}), nil
	}
	type entry struct {
		key   string
		theme ThemeSource
	}
	// a stack buffer covers the usual light and dark pair
	var buf [4]entry
	entries := buf[:0]
	for k, t := range o.Themes {
		if t != nil {
			entries = append(entries, entry{k, t})
		}
	}
	slices.SortFunc(entries, func(a, b entry) int { return strings.Compare(a.key, b.key) })
	if len(entries) == 0 {
		return nil, newError(ErrInvalidTheme, "themes must not be empty")
	}
	defaultColor := o.DefaultColor
	if defaultColor == "" {
		defaultColor = "light"
	}
	role := func(int) themeRole { return roleVariable }
	if !o.NoDefaultColor && defaultColor == "light-dark()" {
		light, dark := -1, -1
		for i := range entries {
			switch entries[i].key {
			case "light":
				light = i
			case "dark":
				dark = i
			}
		}
		if light < 0 || dark < 0 {
			return nil, newError(ErrInvalidTheme, "`themes` must contain `light` and `dark` when defaultColor is 'light-dark()'")
		}
		entries = []entry{entries[light], entries[dark]}
		role = func(int) themeRole { return roleLightDark }
	} else if !o.NoDefaultColor {
		at := -1
		for i, e := range entries {
			if e.key == defaultColor {
				at = i
			}
		}
		if at < 0 {
			return nil, newError(ErrInvalidTheme, "`themes` must contain the defaultColor key `"+defaultColor+"`")
		}
		// move the default theme to the front, keeping the others sorted
		first := entries[at]
		copy(entries[1:at+1], entries[:at])
		entries[0] = first
		role = func(i int) themeRole {
			if i == 0 {
				return roleDefault
			}
			return roleVariable
		}
	}
	var themesBuf [4]resolvedTheme
	themes := themesBuf[:0]
	for i, e := range entries {
		p, err := PrepareTheme(e.theme, prefix)
		if err != nil {
			return nil, err
		}
		themes = append(themes, resolvedTheme{p, e.key, role(i)})
	}
	return cachedSet(themes), nil
}

// forgetPrepared drops the theme sets of a collected theme.
func forgetPrepared(p *PreparedTheme) {
	setCacheMu.Lock()
	defer setCacheMu.Unlock()
	delete(setCache, p)
	delete(preparedIDs, p)
}

// cachedSet reuses a theme set so its style caches stay available. It
// copies themes only when it adds a set, so callers may pass a stack buffer.
func cachedSet(themes []resolvedTheme) *themeSet {
	setCacheMu.Lock()
	defer setCacheMu.Unlock()
	var keyBuf [128]byte
	key := keyBuf[:0]
	for _, t := range themes {
		key = strconv.AppendInt(key, int64(preparedID(t.PreparedTheme)), 10)
		key = append(key, '/')
		key = strconv.AppendInt(key, int64(t.role), 10)
		key = append(key, '/')
		key = strconv.AppendQuote(key, t.color)
		key = append(key, ';')
	}
	list := setCache[themes[0].PreparedTheme]
	if list == nil {
		list = &setList{sets: map[string]*themeSet{}}
		setCache[themes[0].PreparedTheme] = list
	}
	if s := list.sets[string(key)]; s != nil {
		return s
	}
	k := string(key)
	s := &themeSet{themes: slices.Clone(themes)}
	list.sets[k] = s
	list.keys = append(list.keys, k)
	if len(list.keys) > 128 {
		delete(list.sets, list.keys[0])
		list.keys = list.keys[1:]
	}
	return s
}

func styleColor(s *TokenStyle) string {
	if s == nil {
		return ""
	}
	return s.Color
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// rangeToToken converts a range and token ID to a ThemedToken.
func (set *themeSet) rangeToToken(content string, offset, hl int, prefix string) ThemedToken {
	t := ThemedToken{Content: content, Offset: offset}
	first := set.themes[0]
	if first.role == roleSingle {
		style := first.Styles[hl]
		t.Color = orDefault(styleColor(style), first.Fg)
		if style != nil && style.Italic {
			t.FontStyle |= FontItalic
		}
		if style != nil && style.Weight >= 600 {
			t.FontStyle |= FontBold
		}
	} else {
		t.HTMLStyle = set.htmlStyle(hl, prefix)
	}
	t.Type = standardTypes[hl]
	return t
}

// htmlStyle returns the htmlStyle for token ID hl.
func (set *themeSet) htmlStyle(hl int, prefix string) HTMLStyle {
	return set.htmlStyleTable(prefix)[hl]
}

// htmlStyleTable returns the htmlStyle of every token ID. The table is
// built once per prefix and never changes, so callers may keep it.
func (set *themeSet) htmlStyleTable(prefix string) []HTMLStyle {
	set.mu.Lock()
	defer set.mu.Unlock()
	if slots := set.htmlStyles[prefix]; slots != nil {
		return slots
	}
	if set.htmlStyles == nil {
		set.htmlStyles = map[string][]HTMLStyle{}
	}
	slots := make([]HTMLStyle, len(TokenTypes))
	for hl := range slots {
		slots[hl] = set.buildHTMLStyle(hl, prefix)
	}
	set.htmlStyles[prefix] = slots
	return slots
}

// buildHTMLStyle builds the htmlStyle for token ID hl: plain styles for the
// default theme and custom properties for the others, or light-dark().
func (set *themeSet) buildHTMLStyle(hl int, prefix string) HTMLStyle {
	var style HTMLStyle
	if set.themes[0].role == roleLightDark {
		style = lightDarkStyle(set.themes, hl, prefix)
	} else {
		style = HTMLStyle{}
		for _, t := range set.themes {
			s := t.Styles[hl]
			plain := t.role == roleDefault
			name := "color"
			if !plain {
				name = prefix + t.color
			}
			style.set(name, orDefault(orDefault(styleColor(s), t.Fg), "inherit"))
			if s != nil && s.Italic {
				if plain {
					style.set("font-style", "italic")
				} else {
					style.set(prefix+t.color+"-font-style", "italic")
				}
			}
			if s != nil && s.Weight != 0 {
				if plain {
					style.set("font-weight", strconv.Itoa(s.Weight))
				} else {
					style.set(prefix+t.color+"-font-weight", strconv.Itoa(s.Weight))
				}
			}
		}
	}
	return style
}

func lightDarkStyle(themes []resolvedTheme, hl int, prefix string) HTMLStyle {
	light, dark := themes[0], themes[1]
	a, b := light.Styles[hl], dark.Styles[hl]
	ac := orDefault(orDefault(styleColor(a), light.Fg), "currentcolor")
	bc := orDefault(orDefault(styleColor(b), dark.Fg), "currentcolor")
	style := HTMLStyle{}
	if ac == bc {
		style.set("color", ac)
	} else {
		style.set("color", "light-dark("+ac+", "+bc+")")
	}
	ai := a != nil && a.Italic
	bi := b != nil && b.Italic
	if ai && bi {
		style.set("font-style", "italic")
	} else {
		if ai {
			style.set(prefix+"light-font-style", "italic")
		}
		if bi {
			style.set(prefix+"dark-font-style", "italic")
		}
	}
	aw, bw := 0, 0
	if a != nil {
		aw = a.Weight
	}
	if b != nil {
		bw = b.Weight
	}
	if aw != 0 && aw == bw {
		style.set("font-weight", strconv.Itoa(aw))
	} else {
		if aw != 0 {
			style.set(prefix+"light-font-weight", strconv.Itoa(aw))
		}
		if bw != 0 {
			style.set(prefix+"dark-font-weight", strconv.Itoa(bw))
		}
	}
	return style
}

// multiThemeBlob packs a theme set for Wasm HTML mode 2 (see $multiRec in
// emit.wat). It returns nil when a theme lacks a packed table.
func (set *themeSet) multiThemeBlob(prefix string) []byte {
	set.mu.Lock()
	defer set.mu.Unlock()
	if set.blobs == nil {
		set.blobs = map[string][]byte{}
		set.noBlob = map[string]bool{}
	}
	if b := set.blobs[prefix]; b != nil {
		return b
	}
	if set.noBlob[prefix] {
		return nil
	}
	themes := set.themes
	count := len(themes)
	packable := count <= 255
	for _, t := range themes {
		if t.Table == nil {
			packable = false
		}
	}
	if !packable {
		set.noBlob[prefix] = true
		return nil
	}
	names := make([][]byte, count)
	for i, t := range themes {
		if t.role != roleDefault {
			names[i] = []byte(escapeAttribute(prefix + t.color))
		}
	}
	tablesOffset := 32 + 16*count
	namesOffset := tablesOffset + themeTableBytes*count
	spanReserve, prologueReserve, nameBytes := 64, 128, 0
	for _, n := range names {
		spanReserve += 64 + 3*len(n)
		prologueReserve += 48 + 2*len(n)
		nameBytes += len(n)
	}
	blob := make([]byte, namesOffset+nameBytes)
	blob[0] = byte(count)
	if themes[0].role == roleLightDark {
		blob[1] = 1
	}
	le := binary.LittleEndian
	le.PutUint32(blob[4:], uint32(tablesOffset))
	le.PutUint32(blob[8:], uint32(spanReserve))
	le.PutUint32(blob[12:], uint32(prologueReserve))
	le.PutUint32(blob[16:], nextBlobID.Add(1))
	nameOffset := namesOffset
	for i, t := range themes {
		slot := 32 + 16*i
		le.PutUint32(blob[slot:], uint32(nameOffset))
		le.PutUint32(blob[slot+4:], uint32(len(names[i])))
		if t.role == roleDefault {
			le.PutUint32(blob[slot+8:], 1)
		}
		copy(blob[tablesOffset+themeTableBytes*i:], t.Table)
		copy(blob[nameOffset:], names[i])
		nameOffset += len(names[i])
	}
	set.blobs[prefix] = blob
	return blob
}

// multiThemeHTMLOpeners builds HTML openers for theme sets without packed
// RGBA tables.
func (set *themeSet) multiThemeHTMLOpeners(prefix string) []byte {
	set.mu.Lock()
	if b := set.openers[prefix]; b != nil {
		set.mu.Unlock()
		return b
	}
	set.mu.Unlock()
	meta := set.meta(prefix)
	root := meta.rootStyle
	if root == "" {
		root = "background-color:" + meta.bg + ";color:" + meta.fg
	}
	openers := []string{`<pre class="highlights" style="` + escapeAttribute(root) + `"><code>`}
	for hl := 1; hl < len(TokenTypes); hl++ {
		openers = append(openers, `<span style="`+escapeAttribute(set.htmlStyle(hl, prefix).CSS())+`">`)
	}
	blob := packHTMLOpeners(openers)
	set.mu.Lock()
	defer set.mu.Unlock()
	if set.openers == nil {
		set.openers = map[string][]byte{}
	}
	set.openers[prefix] = blob
	return blob
}

// meta returns fg, bg, themeName, and rootStyle for a TokensResult.
func (set *themeSet) meta(prefix string) themeMeta {
	themes := set.themes
	if themes[0].role == roleSingle {
		return themeMeta{fg: themes[0].Fg, bg: themes[0].Bg, themeName: themes[0].Name}
	}
	set.mu.Lock()
	defer set.mu.Unlock()
	if m, ok := set.metas[prefix]; ok {
		return m
	}
	if set.metas == nil {
		set.metas = map[string]themeMeta{}
	}
	m := set.multiMeta(prefix)
	set.metas[prefix] = m
	return m
}

// multiMeta builds the meta of a set of several themes.
func (set *themeSet) multiMeta(prefix string) themeMeta {
	themes := set.themes
	names := make([]string, len(themes))
	for i, t := range themes {
		names[i] = t.Name
	}
	themeName := "highlights-themes " + strings.Join(names, " ")
	if themes[0].role == roleLightDark {
		pair := func(a, b, fallback string) string {
			x, y := orDefault(a, fallback), orDefault(b, fallback)
			if x == y {
				return x
			}
			return "light-dark(" + x + ", " + y + ")"
		}
		return themeMeta{
			fg:        pair(themes[0].Fg, themes[1].Fg, "currentcolor"),
			bg:        pair(themes[0].Bg, themes[1].Bg, "transparent"),
			themeName: themeName,
		}
	}
	fgs := make([]string, len(themes))
	bgs := make([]string, len(themes))
	for i, t := range themes {
		if t.role == roleDefault {
			fgs[i] = orDefault(t.Fg, "inherit")
			bgs[i] = orDefault(t.Bg, "inherit")
		} else {
			fgs[i] = prefix + t.color + ":" + orDefault(t.Fg, "inherit")
			bgs[i] = prefix + t.color + "-bg:" + orDefault(t.Bg, "inherit")
		}
	}
	m := themeMeta{fg: strings.Join(fgs, ";"), bg: strings.Join(bgs, ";"), themeName: themeName}
	if themes[0].role != roleDefault {
		m.rootStyle = m.fg + ";" + m.bg
	}
	return m
}

// tokenRecords views UTF-16 token records in engine memory: pairs of
// little-endian uint32s (endUtf16, tokenID), with tokenID 0xffffffff
// marking a line terminator.
type tokenRecords []byte

func (r tokenRecords) len() int { return len(r) / 8 }

func (r tokenRecords) at(i int) (end int, hl uint32) {
	return int(binary.LittleEndian.Uint32(r[i*8:])), binary.LittleEndian.Uint32(r[i*8+4:])
}

// lineRecordsToTokens converts token records to tokens. code is the
// decoded input; offsets are UTF-16.
func (set *themeSet) lineRecordsToTokens(code *utf16Text, recs tokenRecords, prefix string, maxLineLength, offsetBase int) [][]ThemedToken {
	n := recs.len()
	breaks := 0
	for i := range n {
		if _, hl := recs.at(i); hl == 0xffffffff {
			breaks++
		}
	}
	lines := make([][]ThemedToken, 0, breaks+1)
	// every line's tokens share one array; a line's capacity ends at its
	// length, so appending to it copies instead of overwriting the next
	pool := make([]ThemedToken, 0, n-breaks)
	var styles []HTMLStyle
	first := set.themes[0]
	single := first.role == roleSingle
	start := 0
	for rec := 0; ; rec++ {
		firstRec := rec
		for rec < n {
			if _, hl := recs.at(rec); hl == 0xffffffff {
				break
			}
			rec++
		}
		end := start
		if rec != firstRec {
			end, _ = recs.at(rec - 1)
		}
		var line []ThemedToken
		if maxLineLength > 0 && end-start >= maxLineLength {
			line = []ThemedToken{set.rangeToToken(code.slice(start, end), start+offsetBase, 0, prefix)}
		} else {
			line = pool[len(pool):]
			for at := firstRec; at < rec; at++ {
				end, id := recs.at(at)
				if end <= start {
					continue
				}
				hl := int(id)
				t := ThemedToken{Content: code.slice(start, end), Offset: start + offsetBase}
				if single {
					style := first.Styles[hl]
					t.Color = orDefault(styleColor(style), first.Fg)
					if style != nil && style.Italic {
						t.FontStyle |= FontItalic
					}
					if style != nil && style.Weight >= 600 {
						t.FontStyle |= FontBold
					}
				} else {
					if styles == nil {
						styles = set.htmlStyleTable(prefix)
					}
					t.HTMLStyle = styles[hl]
				}
				t.Type = standardTypes[hl]
				line = append(line, t)
				start = end
			}
			pool = pool[:len(pool)+len(line)]
			line = line[:len(line):len(line)]
		}
		lines = append(lines, line)
		if rec == n {
			break
		}
		start, _ = recs.at(rec)
	}
	return lines
}
