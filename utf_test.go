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
