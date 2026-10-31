// Package wasmrt holds the runtime helpers that generated Wasm-to-Go code
// calls: booleans as numbers, memory growth and fill, traps, and 128-bit
// byte vectors. Built with GOEXPERIMENT=simd on arm64, the vectors use NEON
// through simd/archsimd; elsewhere they are two 64-bit words.
package wasmrt

// PageSize is the Wasm page size in bytes.
const PageSize = 65536

// Trap is the panic value for a Wasm trap.
type Trap string

func (t Trap) Error() string { return "wasm trap: " + string(t) }

// Unreachable traps.
func Unreachable() { panic(Trap("unreachable")) }

// B2u converts a boolean to 0 or 1.
func B2u(b bool) uint32 {
	if b {
		return 1
	}
	return 0
}

// B2u64 converts a boolean to 0 or 1.
func B2u64(b bool) uint64 {
	if b {
		return 1
	}
	return 0
}

// Fill implements memory.fill.
func Fill(mem []byte, dst, v, n uint32) {
	if uint64(dst)+uint64(n) > uint64(len(mem)) {
		panic(Trap("out of bounds memory access"))
	}
	b := mem[dst : dst+n]
	c := byte(v)
	for i := range b {
		b[i] = c
	}
}

// Grow implements memory.grow and returns the old page count or -1.
func Grow(mem *[]byte, n uint32) uint32 {
	old := uint32(len(*mem) / PageSize)
	if uint64(old)+uint64(n) > 65536 {
		return 0xffffffff
	}
	if n > 0 {
		grown := make([]byte, len(*mem)+int(n)*PageSize)
		copy(grown, *mem)
		*mem = grown
	}
	return old
}
