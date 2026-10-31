package highlights

import (
	"strings"
	"unicode/utf8"
)

// TypeError reports an invalid argument shape, like JavaScript's TypeError.
type TypeError struct{ Message string }

func (e *TypeError) Error() string { return e.Message }

// RangeError reports an argument outside its valid range, like
// JavaScript's RangeError.
type RangeError struct{ Message string }

func (e *RangeError) Error() string { return e.Message }

// utf16Len returns the UTF-16 length of valid UTF-8 text.
func utf16Len(s string) int {
	n := 0
	for i := 0; i < len(s); {
		c := s[i]
		if c < 0x80 {
			n++
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
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
		c := t.s[t.b]
		if c < 0x80 {
			t.u++
			t.b++
			continue
		}
		r, size := utf8.DecodeRuneInString(t.s[t.b:])
		if r >= 0x10000 {
			t.u += 2
		} else {
			t.u++
		}
		t.b += size
	}
	return t.b
}

func (t *utf16Text) slice(a, b int) string {
	if a >= b {
		return ""
	}
	start := t.byteOffset(a)
	return t.s[start:t.byteOffset(b)]
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

// toValidText returns text as valid UTF-8, replacing malformed sequences.
func toValidText(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	out, _ := decodeUTF8([]byte(s))
	return out
}
