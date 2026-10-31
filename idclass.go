package highlights

import (
	"sync"
	"unicode"
)

// isIDStart reports Unicode ID_Start: letters, letter numbers, and
// Other_ID_Start, without pattern syntax or whitespace.
func isIDStart(r rune) bool {
	if unicode.Is(unicode.Pattern_Syntax, r) || unicode.Is(unicode.Pattern_White_Space, r) {
		return false
	}
	return unicode.IsLetter(r) || unicode.Is(unicode.Nl, r) || unicode.Is(unicode.Other_ID_Start, r)
}

// isIDContinue reports Unicode ID_Continue plus ZWNJ and ZWJ, which
// ECMAScript identifiers accept.
func isIDContinue(r rune) bool {
	if r == 0x200c || r == 0x200d {
		return true
	}
	if unicode.Is(unicode.Pattern_Syntax, r) || unicode.Is(unicode.Pattern_White_Space, r) {
		return false
	}
	return isIDStart(r) || unicode.In(r, unicode.Mn, unicode.Mc, unicode.Nd, unicode.Pc) ||
		unicode.Is(unicode.Other_ID_Continue, r)
}

var (
	idClassOnce  sync.Once
	idClassTable []byte // per BMP code point: bit 0 ID_Start, bit 1 ID_Continue
)

func buildIDClass() {
	idClassTable = make([]byte, 0x10000)
	for r := rune(0); r < 0x10000; r++ {
		if r >= 0xd800 && r <= 0xdfff {
			continue
		}
		var v byte
		if isIDStart(r) {
			v |= 1
		}
		if isIDContinue(r) {
			v |= 2
		}
		idClassTable[r] = v
	}
}

// idClass decodes the code point at ptr (len UTF-8 bytes) and tests
// ID_Start (shift 0) or ID_Continue (shift 2), as the Wasm imports expect.
func idClass(mem []byte, ptr, n uint32, shift uint32) uint32 {
	b := mem[ptr:]
	var cp rune
	switch n {
	case 2:
		cp = rune(b[0]&0x1f)<<6 | rune(b[1]&0x3f)
	case 3:
		cp = rune(b[0]&0x0f)<<12 | rune(b[1]&0x3f)<<6 | rune(b[2]&0x3f)
	default:
		cp = rune(b[0]&0x07)<<18 | rune(b[1]&0x3f)<<12 | rune(b[2]&0x3f)<<6 | rune(b[3]&0x3f)
	}
	// a lead byte above 0xf4 decodes past the last code point
	if cp > 0x10ffff {
		return 0
	}
	if cp > 0xffff || (cp >= 0xd800 && cp <= 0xdfff) {
		var yes bool
		if shift == 0 {
			yes = isIDStart(cp)
		} else {
			yes = isIDContinue(cp)
		}
		if yes {
			return 1
		}
		return 0
	}
	idClassOnce.Do(buildIDClass)
	bit := byte(1)
	if shift != 0 {
		bit = 2
	}
	if idClassTable[cp]&bit != 0 {
		return 1
	}
	return 0
}
