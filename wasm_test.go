package highlights_test

// Ports of the engine tests in vendor/pierre-highlights/test/wasm_test.ts.

import (
	"testing"

	highlights "highlights"
	"highlights/internal/engine"
	"highlights/internal/fixtures/kwtest"
)

func TestHighlighterWritesInputAtMemInput(t *testing.T) {
	if got := int(kwtest.New().ExportGlobalInput()); got != highlights.InputPtr {
		t.Fatalf("$mem.input = %d, InputPtr = %d", got, highlights.InputPtr)
	}
}

func TestLiveStateHashingAndComparisonIgnoreBytesBeyondTheState(t *testing.T) {
	m := engine.New()
	bytes := m.Mem
	a := len(bytes) - 160
	b := len(bytes) - 80
	for length := 0; length <= 64; length++ {
		for _, alignment := range []int{0, 1, 7} {
			input := make([]byte, length)
			for i := range input {
				input[i] = byte(i*37 + 129)
			}
			for i := a; i < b; i++ {
				bytes[i] = 0xa5
			}
			for i := b; i < len(bytes); i++ {
				bytes[i] = 0x5a
			}
			copy(bytes[a+alignment:], input)
			copy(bytes[b+alignment:], input)
			expected := uint64(0xcbf29ce484222325)
			for at := 0; at < length; at += 8 {
				var lane uint64
				for i := 0; i < min(8, length-at); i++ {
					lane |= uint64(input[at+i]) << (i * 8)
				}
				expected = (expected ^ lane) * 0x100000001b3
			}
			expected = (expected ^ (expected >> 33)) * 0xff51afd7ed558ccd
			expected = (expected ^ (expected >> 33)) * 0xc4ceb9fe1a85ec53
			expected ^= expected >> 33
			if got := m.FlvHash(uint32(a+alignment), uint32(length)); got != expected {
				t.Fatalf("hash(a, %d) = %x, want %x", length, got, expected)
			}
			if got := m.FlvHash(uint32(b+alignment), uint32(length)); got != expected {
				t.Fatalf("hash(b, %d) = %x, want %x", length, got, expected)
			}
			if m.FlvBytesEq(uint32(a+alignment), uint32(b+alignment), uint32(length)) != 1 {
				t.Fatalf("equal(%d) = 0", length)
			}
			for i := 0; i < length; i++ {
				bytes[b+alignment+i] ^= 0xff
				if m.FlvBytesEq(uint32(a+alignment), uint32(b+alignment), uint32(length)) != 0 {
					t.Fatalf("equal(%d) ignores byte %d", length, i)
				}
				bytes[b+alignment+i] ^= 0xff
			}
		}
	}
	n := uint32(len(bytes))
	if m.FlvHash(n, 0) != m.FlvHash(uint32(a), 0) {
		t.Fatal("empty hashes differ")
	}
	if m.FlvBytesEq(n, n, 0) != 1 || m.FlvBytesEq(n-16, n-16, 16) != 1 {
		t.Fatal("equal at the end of memory")
	}
}
