package kwtest

// Ports of the keyword and lowercase tests in
// vendor/pierre-highlights/test/wasm_test.ts.

import (
	"bytes"
	"testing"
)

var words = func() []string {
	var out []string
	for i := 0; i < 30; i++ {
		out = append(out, "abcdefghijklmnopqrstuvwxyzabcde"[:i+2])
	}
	return out
}()

func TestKeywordSimdComparesEveryByteAtLengths2Through31(t *testing.T) {
	m := New()
	input := int(m.ExportGlobalInput())
	for _, word := range words {
		for _, padding := range []byte{0, 0xff} {
			for i := 0; i < 64; i++ {
				m.Mem[input+i] = padding
			}
			copy(m.Mem[input:], word)
			if got := int32(m.ExportLookup(uint32(len(word)))); got != int32(len(word)) {
				t.Fatalf("%s: lookup = %d", word, got)
			}
			for i := 0; i < len(word); i++ {
				m.Mem[input+i] ^= 32
				if got := int32(m.ExportLookup(uint32(len(word)))); got != -1 {
					t.Fatalf("%s, byte %d: lookup = %d", word, i, got)
				}
				m.Mem[input+i] ^= 32
			}
		}
	}
	for _, n := range []uint32{0, 1, 32, 64} {
		if got := int32(m.ExportLookup(n)); got != -1 {
			t.Fatalf("length %d: lookup = %d", n, got)
		}
	}
}

func TestLowercaseSimdPreservesNonLettersAndStaysInsideScratch(t *testing.T) {
	m := New()
	input := int(m.ExportGlobalInput())
	dst := int(m.ExportGlobalScratch())
	for length := 0; length <= 32; length++ {
		for first := 0; first < 256; first += 16 {
			in := make([]byte, length)
			for i := range in {
				in[i] = byte(first + i)
			}
			for i := 0; i < 80; i++ {
				m.Mem[dst+i] = 0xa5
			}
			copy(m.Mem[input:], in)
			want := uint32(length)
			if length > 31 {
				want = 0
			}
			if got := m.ExportLower(uint32(length)); got != want {
				t.Fatalf("length %d: lower = %d", length, got)
			}
			if length <= 31 {
				lower := make([]byte, length)
				for i, c := range in {
					if c >= 'A' && c <= 'Z' {
						c += 32
					}
					lower[i] = c
				}
				if !bytes.Equal(m.Mem[dst:dst+length], lower) {
					t.Fatalf("length %d, first %d: %v", length, first, m.Mem[dst:dst+length])
				}
			} else if !bytes.Equal(m.Mem[dst:dst+64], bytes.Repeat([]byte{0xa5}, 64)) {
				t.Fatal("long input wrote scratch")
			}
			if !bytes.Equal(m.Mem[dst+64:dst+80], bytes.Repeat([]byte{0xa5}, 16)) {
				t.Fatal("lowercase wrote past scratch")
			}
		}
	}
}
