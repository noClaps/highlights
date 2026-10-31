package highlights

import (
	"strings"
	"unicode/utf8"
)

// decodeUnit decodes the code point at s[i] and returns its UTF-16 length
// and byte size. A three-byte surrogate encoding (WTF-8) is one unit, and
// any other malformed byte counts as one replacement character.
func decodeUnit(s string, i int) (units, size int) {
	c := s[i]
	if c < 0x80 {
		return 1, 1
	}
	r, size := utf8.DecodeRuneInString(s[i:])
	if r != utf8.RuneError || size > 1 {
		if r >= 0x10000 {
			return 2, size
		}
		return 1, size
	}
	if c == 0xed && i+2 < len(s) && s[i+1] >= 0xa0 && s[i+1] <= 0xbf && s[i+2] >= 0x80 && s[i+2] <= 0xbf {
		return 1, 3
	}
	return 1, 1
}

// load64 reads eight bytes of s at i as a little-endian word; the compiler
// turns it into one load.
func load64(s string, i int) uint64 {
	_ = s[i+7]
	return uint64(s[i]) | uint64(s[i+1])<<8 | uint64(s[i+2])<<16 | uint64(s[i+3])<<24 |
		uint64(s[i+4])<<32 | uint64(s[i+5])<<40 | uint64(s[i+6])<<48 | uint64(s[i+7])<<56
}

// utf16Len returns the UTF-16 length of UTF-8 or WTF-8 text.
func utf16Len(s string) int {
	n := 0
	for i := 0; i < len(s); {
		// eight ASCII bytes are eight units
		if i+8 <= len(s) && load64(s, i)&0x8080808080808080 == 0 {
			n += 8
			i += 8
			continue
		}
		if s[i] < 0x80 {
			n++
			i++
			continue
		}
		u, size := decodeUnit(s, i)
		n += u
		i += size
	}
	return n
}

// utf16Text slices valid UTF-8 text by UTF-16 offsets. Lookups that move
// forward resume from the previous one.
type utf16Text struct {
	s     string
	ascii bool
	u, b  int // cursor: UTF-16 offset u is at byte b
}

func newUTF16Text(s string) *utf16Text {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			ascii = false
			break
		}
	}
	return &utf16Text{s: s, ascii: ascii}
}

// byteOffset returns the byte index of UTF-16 offset u. An offset inside a
// surrogate pair rounds up to the end of its code point.
func (t *utf16Text) byteOffset(u int) int {
	if t.ascii {
		return min(u, len(t.s))
	}
	if u < t.u {
		t.u, t.b = 0, 0
	}
	for t.u < u && t.b < len(t.s) {
		// eight ASCII bytes are eight units
		if t.u+8 <= u && t.b+8 <= len(t.s) && load64(t.s, t.b)&0x8080808080808080 == 0 {
			t.u += 8
			t.b += 8
			continue
		}
		c := t.s[t.b]
		if c < 0x80 {
			t.u++
			t.b++
			continue
		}
		u, size := decodeUnit(t.s, t.b)
		t.u += u
		t.b += size
	}
	return t.b
}

func (t *utf16Text) slice(a, b int) string {
	if a >= b {
		return ""
	}
	if t.ascii {
		return t.s[min(a, len(t.s)):min(b, len(t.s))]
	}
	start := t.byteOffset(a)
	var head string
	if t.u > a {
		// a falls inside a surrogate pair: keep its low half
		_, lo := splitPair(t.s, t.b-4)
		head = lo
	}
	end := t.byteOffset(b)
	if t.u > b {
		hi, _ := splitPair(t.s, t.b-4)
		return head + t.s[start:end-4] + hi
	}
	return head + t.s[start:end]
}

// splitPair returns the WTF-8 halves of the four-byte code point at s[i].
func splitPair(s string, i int) (hi, lo string) {
	r, _ := utf8.DecodeRuneInString(s[i:])
	r -= 0x10000
	enc := func(u rune) string {
		return string([]byte{0xed, byte(0x80 | (u>>6)&0x3f), byte(0x80 | u&0x3f)})
	}
	return enc(0xd800 + r>>10), enc(0xdc00 + r&0x3ff)
}

// decodeUTF8 decodes bytes as the WHATWG UTF-8 decoder does, replacing each
// maximal invalid subpart with U+FFFD. It reports whether any replacement
// character is present in the result.
func decodeUTF8(b []byte) (string, bool) {
	if utf8.Valid(b) {
		s := string(b)
		return s, strings.Contains(s, "�")
	}
	var sb strings.Builder
	sb.Grow(len(b) + 8)
	for i := 0; i < len(b); {
		c := b[i]
		if c < 0x80 {
			sb.WriteByte(c)
			i++
			continue
		}
		n, lo, hi := 0, byte(0x80), byte(0xbf)
		switch {
		case c >= 0xc2 && c <= 0xdf:
			n = 1
		case c == 0xe0:
			n, lo = 2, 0xa0
		case c >= 0xe1 && c <= 0xec, c == 0xee, c == 0xef:
			n = 2
		case c == 0xed:
			n, hi = 2, 0x9f
		case c == 0xf0:
			n, lo = 3, 0x90
		case c >= 0xf1 && c <= 0xf3:
			n = 3
		case c == 0xf4:
			n, hi = 3, 0x8f
		default:
			sb.WriteString("�")
			i++
			continue
		}
		j := i + 1
		ok := true
		for k := 0; k < n; k++ {
			if j >= len(b) || b[j] < lo || b[j] > hi {
				ok = false
				break
			}
			j++
			lo, hi = 0x80, 0xbf
		}
		if !ok {
			sb.WriteString("�")
			i = j
			continue
		}
		sb.Write(b[i:j])
		i = j
	}
	return sb.String(), true
}

// toLiveText keeps UTF-8 and WTF-8 surrogate encodings and replaces any
// other malformed byte with U+FFFD, matching what a JavaScript string can
// hold.
func toLiveText(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	var sb strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		if c < 0x80 {
			sb.WriteByte(c)
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r != utf8.RuneError || size > 1 {
			sb.WriteString(s[i : i+size])
			i += size
			continue
		}
		if _, n := decodeUnit(s, i); n == 3 {
			sb.WriteString(s[i : i+3])
			i += 3
			continue
		}
		sb.WriteString("\uFFFD")
		i++
	}
	return sb.String()
}

// surrogateAt returns the surrogate a three-byte WTF-8 sequence at s[i]
// encodes, or 0.
func surrogateAt(s string, i int) rune {
	if i+2 < len(s) && s[i] == 0xed && s[i+1] >= 0xa0 && s[i+1] <= 0xbf && s[i+2] >= 0x80 && s[i+2] <= 0xbf {
		return 0xd000 | rune(s[i+1]&0x3f)<<6 | rune(s[i+2]&0x3f)
	}
	return 0
}

// normalizeText prepares text the way JavaScript strings behave. content
// keeps each lone surrogate as WTF-8, for token content; engine replaces
// each with U+FFFD, as TextEncoder does, for the lexer. Both join WTF-8
// surrogate pairs and replace other malformed bytes as the WHATWG decoder
// does, so their UTF-16 offsets agree.
func normalizeText(s string) (content, engine string) {
	if utf8.ValidString(s) {
		return s, s
	}
	var c, e strings.Builder
	for i := 0; i < len(s); {
		if hi := surrogateAt(s, i); hi != 0 {
			if lo := surrogateAt(s, i+3); hi < 0xdc00 && lo >= 0xdc00 {
				r := 0x10000 + (hi-0xd800)<<10 + (lo - 0xdc00)
				c.WriteRune(r)
				e.WriteRune(r)
				i += 6
				continue
			}
			c.WriteString(s[i : i+3])
			e.WriteString("�")
			i += 3
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r != utf8.RuneError || size > 1 {
			c.WriteString(s[i : i+size])
			e.WriteString(s[i : i+size])
			i += size
			continue
		}
		_, w := maximalSubpart(s[i:])
		c.WriteString("\uFFFD")
		e.WriteString("\uFFFD")
		i += w
	}
	return c.String(), e.String()
}

// maximalSubpart returns U+FFFD and the length of the maximal invalid
// subpart at the start of s, as the WHATWG decoder defines it.
func maximalSubpart(s string) (rune, int) {
	c := s[0]
	n, lo, hi := 0, byte(0x80), byte(0xbf)
	switch {
	case c >= 0xc2 && c <= 0xdf:
		n = 1
	case c == 0xe0:
		n, lo = 2, 0xa0
	case c >= 0xe1 && c <= 0xec, c == 0xee, c == 0xef:
		n = 2
	case c == 0xed:
		n, hi = 2, 0x9f
	case c == 0xf0:
		n, lo = 3, 0x90
	case c >= 0xf1 && c <= 0xf3:
		n = 3
	case c == 0xf4:
		n, hi = 3, 0x8f
	default:
		return utf8.RuneError, 1
	}
	j := 1
	for k := 0; k < n; k++ {
		if j >= len(s) || s[j] < lo || s[j] > hi {
			break
		}
		j++
		lo, hi = 0x80, 0xbf
	}
	return utf8.RuneError, j
}

// engineText is the lexer form of normalizeText.
func engineText(s string) string {
	_, e := normalizeText(s)
	return e
}

// joinSurrogatePairs joins WTF-8 surrogate pairs into UTF-8.
func joinSurrogatePairs(s string) string {
	if utf8.ValidString(s) || !strings.Contains(s, "\xed") {
		return s
	}
	var sb strings.Builder
	for i := 0; i < len(s); {
		if hi := surrogateAt(s, i); hi >= 0xd800 && hi < 0xdc00 {
			if lo := surrogateAt(s, i+3); lo >= 0xdc00 {
				sb.WriteRune(0x10000 + (hi-0xd800)<<10 + (lo - 0xdc00))
				i += 6
				continue
			}
		}
		sb.WriteByte(s[i])
		i++
	}
	return sb.String()
}
