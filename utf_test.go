package highlights

import (
	"math/rand"
	"testing"
)

func TestUTF16LenCountsEveryUnit(t *testing.T) {
	// the count one code point at a time, without the ASCII fast path
	slow := func(s string) int {
		n := 0
		for i := 0; i < len(s); {
			u, size := decodeUnit(s, i)
			n += u
			i += size
		}
		return n
	}
	pieces := []string{"a", "z", "\n", "é", "日", "🙂", "\xed\xa0\x80", "\xed\xbf\xbf", "\xff", "\xe6", "\xf0\x9f"}
	r := rand.New(rand.NewSource(1))
	for range 20000 {
		var s []byte
		for n := r.Intn(40); n > 0; n-- {
			if r.Intn(3) == 0 {
				s = append(s, pieces[r.Intn(len(pieces))]...)
			} else {
				s = append(s, "abcdefgh"[:r.Intn(9)]...)
			}
		}
		if got, want := utf16Len(string(s)), slow(string(s)); got != want {
			t.Fatalf("utf16Len(%q) = %d, want %d", s, got, want)
		}
	}
}

func TestByteOffsetMatchesAWalk(t *testing.T) {
	// the byte offset of UTF-16 offset u, one code point at a time
	walk := func(s string, u int) int {
		n, b := 0, 0
		for n < u && b < len(s) {
			units, size := decodeUnit(s, b)
			n += units
			b += size
		}
		return b
	}
	pieces := []string{"é", "日", "🙂", "\xed\xa0\x80", "\xff"}
	r := rand.New(rand.NewSource(2))
	for range 2000 {
		var s []byte
		for n := r.Intn(30); n > 0; n-- {
			if r.Intn(4) == 0 {
				s = append(s, pieces[r.Intn(len(pieces))]...)
			} else {
				s = append(s, "abcdefghijklmnop"[:r.Intn(17)]...)
			}
		}
		text := newUTF16Text(string(s))
		total := utf16Len(string(s))
		// offsets mostly ascending, as tokens arrive, with a few going back
		u := 0
		for range 40 {
			if r.Intn(8) == 0 {
				u = r.Intn(total + 2)
			} else {
				u = min(total+1, u+r.Intn(12))
			}
			if got, want := text.byteOffset(u), walk(string(s), u); got != want {
				t.Fatalf("byteOffset(%q, %d) = %d, want %d", s, u, got, want)
			}
		}
	}
}
