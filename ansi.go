package highlights

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

const (
	ansiBold      = 1
	ansiDim       = 2
	ansiItalic    = 4
	ansiUnderline = 8
	ansiReverse   = 16
	ansiStrike    = 32
)

// Flags that SGR codes 1-9 set. Codes 21-29 clear the flag of code n - 20.
var sgrFlags = []int{0, ansiBold, ansiDim, ansiItalic, ansiUnderline, 0, 0, ansiReverse, 0, ansiStrike}

var cubeLevels = []int{0, 95, 135, 175, 215, 255}

// ansiStyle holds the token fields for one SGR state.
type ansiStyle struct {
	color, bgColor string
	fontStyle      FontStyle
	htmlStyle      HTMLStyle
}

// ansiState is SGR state that carries across lines and stream chunks.
// Colors are -1 for the theme default, 0-255 for palette indexes, or
// 0x1000000 | rgb.
type ansiState struct {
	fg, bg, flags int
	styles        map[[3]int]*ansiStyle
}

func newAnsiState() *ansiState {
	return &ansiState{fg: -1, bg: -1, styles: map[[3]int]*ansiStyle{}}
}

func isAnsi(lang string) bool { return strings.ToLower(lang) == "ansi" }

func paletteColor(theme resolvedTheme, color int) string {
	if color < 16 {
		return theme.Ansi()[color]
	}
	rgb := color & 0xffffff
	if color < 232 {
		i := color - 16
		rgb = cubeLevels[i/36]<<16 | cubeLevels[(i/6)%6]<<8 | cubeLevels[i%6]
	} else if color < 256 {
		rgb = (8 + (color-232)*10) * 0x10101
	}
	return fmt.Sprintf("#%06x", rgb)
}

// matchEscape matches an escape sequence at i: a CSI with parameters and
// final byte, an OSC ending in BEL or ST, or a short escape such as `ESC(B`.
// It returns the end, the CSI parameters, and the CSI final byte.
func matchEscape(code string, i int) (end int, params string, final byte, ok bool) {
	if i >= len(code) || code[i] != 0x1b {
		return 0, "", 0, false
	}
	j := i + 1
	if j < len(code) && code[j] == '[' {
		p := j + 1
		for p < len(code) && code[p] >= 0x30 && code[p] <= 0x3f {
			p++
		}
		q := p
		for q < len(code) && code[q] >= 0x20 && code[q] <= 0x2f {
			q++
		}
		if q < len(code) && code[q] >= 0x40 && code[q] <= 0x7e {
			return q + 1, code[j+1 : p], code[q], true
		}
	}
	if j < len(code) && code[j] == ']' {
		p := j + 1
		for p < len(code) && code[p] != 0x07 && code[p] != 0x1b && code[p] != '\n' {
			p++
		}
		if p < len(code) && code[p] == 0x07 {
			return p + 1, "", 0, true
		}
		if p+1 < len(code) && code[p] == 0x1b && code[p+1] == '\\' {
			return p + 2, "", 0, true
		}
	}
	p := j
	for p < len(code) && code[p] >= 0x20 && code[p] <= 0x2f {
		p++
	}
	if p < len(code) && code[p] >= 0x30 && code[p] <= 0x7e {
		return p + 1, "", 0, true
	}
	return 0, "", 0, false
}

// ansiTokens splits text with ANSI escape sequences into token lines, as
// Shiki does for `ansi`. Tokens omit the sequences; offsets are UTF-16
// input offsets.
func ansiTokens(code string, set *themeSet, prefix string, maxLineLength int, state *ansiState, offsetBase int) [][]ThemedToken {
	var lines [][]ThemedToken
	themes := set.themes
	line := []ThemedToken{}
	var style *ansiStyle
	last := -1 // index of the last token in line
	var lastStyle *ansiStyle
	// byte positions with matching UTF-16 offsets
	pos, upos := 0, 0
	advance := func(from, to int) int { return utf16Len(code[from:to]) }
	for {
		lf := strings.IndexByte(code[pos:], '\n')
		if lf >= 0 {
			lf += pos
		}
		end := len(code)
		if lf >= 0 {
			end = lf
		}
		if lf > pos && code[lf-1] == '\r' {
			end--
		}
		uend := upos + advance(pos, end)
		unstyled := maxLineLength > 0 && uend-upos >= maxLineLength
		for pos < end {
			esc := strings.IndexByte(code[pos:end], 0x1b)
			textEnd := end
			if esc >= 0 {
				textEnd = pos + esc
			}
			next := textEnd
			var params string
			hasParams := false
			if textEnd < end {
				if e, p, final, ok := matchEscape(code, textEnd); ok {
					next = e
					// private parameters such as `ESC[>4;2m` are not SGR
					if final == 'm' && !(len(p) > 0 && p[0] >= 0x3c) {
						params = p
						hasParams = true
					}
				} else {
					// keep an escape that starts no sequence as text
					textEnd++
					next = textEnd
				}
			}
			if textEnd > pos {
				text := code[pos:textEnd]
				if unstyled {
					if last < 0 {
						line = append(line, set.rangeToToken(text, upos+offsetBase, 0, prefix))
						last = len(line) - 1
					} else {
						line[last].Content += text
					}
				} else {
					if style == nil {
						style = ansiStyleFor(state, themes, prefix)
					}
					if last >= 0 && lastStyle == style {
						line[last].Content += text
					} else {
						t := ThemedToken{Content: text, Offset: upos + offsetBase, Color: style.color, BgColor: style.bgColor, FontStyle: style.fontStyle, HTMLStyle: style.htmlStyle}
						line = append(line, t)
						last = len(line) - 1
						lastStyle = style
					}
				}
			}
			if hasParams {
				applySGR(state, params)
				style = nil
			}
			upos += advance(pos, next)
			pos = next
		}
		lines = append(lines, line)
		if lf < 0 {
			return lines
		}
		upos += advance(pos, lf+1)
		pos = lf + 1
		line = []ThemedToken{}
		last = -1
	}
}

func ansiStyleFor(state *ansiState, themes []resolvedTheme, prefix string) *ansiStyle {
	key := [3]int{state.fg, state.bg, state.flags}
	if s := state.styles[key]; s != nil {
		return s
	}
	fg, bg, flags := state.fg, state.bg, state.flags
	fontStyle := FontStyle(0)
	if flags&ansiItalic != 0 {
		fontStyle |= FontItalic
	}
	if flags&ansiBold != 0 {
		fontStyle |= FontBold
	}
	if flags&ansiUnderline != 0 {
		fontStyle |= FontUnderline
	}
	if flags&ansiStrike != 0 {
		fontStyle |= FontStrikethrough
	}
	type pair struct{ color, bg string }
	colors := make([]pair, len(themes))
	for i, theme := range themes {
		color := theme.Fg
		if fg >= 0 {
			color = paletteColor(theme, fg)
		}
		bgColor := ""
		if bg >= 0 {
			bgColor = paletteColor(theme, bg)
		}
		if flags&ansiReverse != 0 {
			bgColor = color
			if bg < 0 {
				color = theme.Bg
			} else {
				color = paletteColor(theme, bg)
			}
		}
		if flags&ansiDim != 0 && color != "" {
			switch {
			case !strings.HasPrefix(color, "#"):
				color = "color-mix(in srgb, " + color + " 50%, transparent)"
			case len(color) == 9:
				a, _ := strconv.ParseUint(color[7:], 16, 8)
				color = color[:7] + fmt.Sprintf("%02x", int(math.Floor(float64(a)/2+0.5)))
			default:
				color += "80"
			}
		}
		colors[i] = pair{color, bgColor}
	}
	s := &ansiStyle{}
	if themes[0].role == roleSingle {
		s.color, s.bgColor, s.fontStyle = colors[0].color, colors[0].bg, fontStyle
	} else {
		var font []StyleProperty
		if fontStyle&FontItalic != 0 {
			font = append(font, StyleProperty{"font-style", "italic"})
		}
		if fontStyle&FontBold != 0 {
			font = append(font, StyleProperty{"font-weight", "bold"})
		}
		if fontStyle&(FontUnderline|FontStrikethrough) != 0 {
			deco := "line-through"
			if fontStyle&(FontUnderline|FontStrikethrough) == FontUnderline|FontStrikethrough {
				deco = "underline line-through"
			} else if fontStyle&FontUnderline != 0 {
				deco = "underline"
			}
			font = append(font, StyleProperty{"text-decoration", deco})
		}
		style := HTMLStyle{}
		if themes[0].role == roleLightDark {
			a, ab := colors[0].color, colors[0].bg
			b, bb := colors[1].color, colors[1].bg
			ac, bc := orDefault(a, "currentcolor"), orDefault(b, "currentcolor")
			if ac == bc {
				style.set("color", ac)
			} else {
				style.set("color", "light-dark("+ac+", "+bc+")")
			}
			if ab != "" || bb != "" {
				x, y := orDefault(ab, "transparent"), orDefault(bb, "transparent")
				if x == y {
					style.set("background-color", x)
				} else {
					style.set("background-color", "light-dark("+x+", "+y+")")
				}
			}
			for _, f := range font {
				style.set(f.Name, f.Value)
			}
		} else {
			for i, theme := range themes {
				plain := theme.role == roleDefault
				name := prefix + theme.color
				if plain {
					style.set("color", orDefault(colors[i].color, "inherit"))
				} else {
					style.set(name, orDefault(colors[i].color, "inherit"))
				}
				if colors[i].bg != "" {
					if plain {
						style.set("background-color", colors[i].bg)
					} else {
						style.set(name+"-bg", colors[i].bg)
					}
				}
				for _, f := range font {
					if plain {
						style.set(f.Name, f.Value)
					} else {
						style.set(name+"-"+f.Name, f.Value)
					}
				}
			}
		}
		s.htmlStyle = style
	}
	state.styles[key] = s
	return s
}

// sgrNumber converts an SGR parameter as Number() does; params hold only
// bytes 0x30-0x3f, so anything but digits is NaN.
func sgrNumber(s string) float64 {
	if s == "" {
		return 0
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return math.NaN()
		}
	}
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

func applySGR(state *ansiState, params string) {
	codes := strings.Split(params, ";")
	at := func(i int) (string, bool) {
		if i < len(codes) {
			return codes[i], true
		}
		return "", false
	}
	num := func(i int) float64 {
		if s, ok := at(i); ok {
			return sgrNumber(s)
		}
		return math.NaN()
	}
	for i := 0; i < len(codes); i++ {
		n := sgrNumber(codes[i])
		switch {
		case n == 0:
			state.fg, state.bg, state.flags = -1, -1, 0
		case n < 10:
			if n == math.Trunc(n) {
				state.flags |= sgrFlags[int(n)]
			}
		case n > 20 && n < 30:
			clearBits := 0
			if n == math.Trunc(n) {
				clearBits = sgrFlags[int(n)-20]
			}
			if n == 22 {
				clearBits |= ansiBold
			}
			state.flags &^= clearBits
		case n >= 30 && n < 38:
			state.fg = int(n) - 30
		case n == 39:
			state.fg = -1
		case n >= 40 && n < 48:
			state.bg = int(n) - 40
		case n == 49:
			state.bg = -1
		case n >= 90 && n < 98:
			state.fg = int(n) - 82
		case n >= 100 && n < 108:
			state.bg = int(n) - 92
		case n == 38 || n == 48:
			i++
			mode, _ := at(i)
			color := -1
			if mode == "5" {
				i++
				index := num(i)
				if index == math.Trunc(index) && index < 256 {
					color = int(index)
				}
			} else if mode == "2" {
				r, g, b := num(i+1), num(i+2), num(i+3)
				i += 3
				if r >= 0 && g >= 0 && b >= 0 {
					color = 0x1000000 | int(math.Min(r, 255))<<16 | int(math.Min(g, 255))<<8 | int(math.Min(b, 255))
				}
			}
			if color >= 0 {
				if n == 38 {
					state.fg = color
				} else {
					state.bg = color
				}
			}
		}
	}
}

// ansiHTML renders `ansi` input as a `<pre>` fragment.
func ansiHTML(code string, set *themeSet, prefix string) string {
	meta := set.meta(prefix)
	themes := set.themes
	tokenSet := set
	if themes[0].role == roleSingle {
		// tag a single theme as inline so every token gets htmlStyle
		single := themes[0]
		single.role = roleDefault
		tokenSet = &themeSet{themes: []resolvedTheme{single}}
	}
	lines := ansiTokens(code, tokenSet, prefix, 0, newAnsiState(), 0)
	root := meta.rootStyle
	if root == "" {
		if meta.bg != "" {
			root += "background-color:" + meta.bg + ";"
		}
		if meta.fg != "" {
			root += "color:" + meta.fg
		}
	}
	var sb strings.Builder
	sb.WriteString(`<pre class="highlights" style="` + escapeAttribute(root) + `"><code>`)
	lf := -1
	for i, line := range lines {
		if i > 0 {
			lf += 1 + strings.IndexByte(code[lf+1:], '\n')
			if lf > 0 && code[lf-1] == '\r' {
				sb.WriteString("\r\n")
			} else {
				sb.WriteString("\n")
			}
		}
		for _, t := range line {
			sb.WriteString(`<span style="` + escapeAttribute(t.HTMLStyle.CSS()) + `">`)
			sb.WriteString(htmlEscaper.Replace(t.Content))
			sb.WriteString("</span>")
		}
	}
	sb.WriteString("</code></pre>")
	return sb.String()
}

var htmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
