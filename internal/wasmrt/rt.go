// Package wasmrt holds the runtime helpers that generated Wasm-to-Go code
// calls: little-endian memory access, booleans, and 128-bit byte vectors.
package wasmrt

import (
	"encoding/binary"
)

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

// Copy implements memory.copy.
func Copy(mem []byte, dst, src, n uint32) {
	if uint64(src)+uint64(n) > uint64(len(mem)) || uint64(dst)+uint64(n) > uint64(len(mem)) {
		panic(Trap("out of bounds memory access"))
	}
	copy(mem[dst:dst+n], mem[src:src+n])
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

// V128 is a 128-bit vector stored as two little-endian 64-bit lanes.
type V128 struct{ Lo, Hi uint64 }

const (
	ones = 0x0101010101010101
	high = 0x8080808080808080
	low7 = 0x7f7f7f7f7f7f7f7f
)

// LdV implements v128.load from the first 16 bytes of b.
func LdV(b []byte) V128 {
	_ = b[15]
	return V128{binary.LittleEndian.Uint64(b), binary.LittleEndian.Uint64(b[8:])}
}

// StV implements v128.store to the first 16 bytes of b.
func StV(b []byte, v V128) {
	_ = b[15]
	binary.LittleEndian.PutUint64(b, v.Lo)
	binary.LittleEndian.PutUint64(b[8:], v.Hi)
}

// Splat8 implements i8x16.splat.
func Splat8(x uint32) V128 {
	v := uint64(byte(x)) * ones
	return V128{v, v}
}

// Or implements v128.or.
func (a V128) Or(b V128) V128 { return V128{a.Lo | b.Lo, a.Hi | b.Hi} }

// And implements v128.and.
func (a V128) And(b V128) V128 { return V128{a.Lo & b.Lo, a.Hi & b.Hi} }

// Xor implements v128.xor.
func (a V128) Xor(b V128) V128 { return V128{a.Lo ^ b.Lo, a.Hi ^ b.Hi} }

// AndNot implements v128.andnot.
func (a V128) AndNot(b V128) V128 { return V128{a.Lo &^ b.Lo, a.Hi &^ b.Hi} }

// Not implements v128.not.
func (a V128) Not() V128 { return V128{^a.Lo, ^a.Hi} }

// AnyTrue implements v128.any_true.
func (a V128) AnyTrue() bool { return a.Lo|a.Hi != 0 }

// zeroBytes sets 0xff in each byte of x that is zero.
func zeroBytes(x uint64) uint64 {
	t := ((x & low7) + low7) | x
	return ((^t & high) >> 7) * 0xff
}

// Eq8 implements i8x16.eq.
func (a V128) Eq8(b V128) V128 { return V128{zeroBytes(a.Lo ^ b.Lo), zeroBytes(a.Hi ^ b.Hi)} }

// Ne8 implements i8x16.ne.
func (a V128) Ne8(b V128) V128 { return a.Eq8(b).Not() }

func sub8(a, b uint64) uint64 { return ((a | high) - (b &^ high)) ^ ((a ^ ^b) & high) }

// Sub8 implements i8x16.sub.
func (a V128) Sub8(b V128) V128 { return V128{sub8(a.Lo, b.Lo), sub8(a.Hi, b.Hi)} }

func add8(a, b uint64) uint64 { return ((a &^ high) + (b &^ high)) ^ ((a ^ b) & high) }

// Add8 implements i8x16.add.
func (a V128) Add8(b V128) V128 { return V128{add8(a.Lo, b.Lo), add8(a.Hi, b.Hi)} }

// geMask sets 0xff in each byte lane where x >= y, unsigned. For lanes
// whose high bits differ, x's high bit decides; otherwise the borrow-free
// difference of the low seven bits does.
func geMask(x, y uint64) uint64 {
	d := (x | high) - (y &^ high)
	m := ((x &^ y) | (^(x ^ y) & d)) & high
	return (m >> 7) * 0xff
}

// GeU8 implements i8x16.ge_u.
func (a V128) GeU8(b V128) V128 { return V128{geMask(a.Lo, b.Lo), geMask(a.Hi, b.Hi)} }

// LeU8 implements i8x16.le_u.
func (a V128) LeU8(b V128) V128 { return b.GeU8(a) }

// LtU8 implements i8x16.lt_u.
func (a V128) LtU8(b V128) V128 { return a.GeU8(b).Not() }

// GtU8 implements i8x16.gt_u.
func (a V128) GtU8(b V128) V128 { return b.GeU8(a).Not() }

// GtS8 implements i8x16.gt_s: signed lanes compare as unsigned once their
// sign bits flip.
func (a V128) GtS8(b V128) V128 {
	f := V128{high, high}
	return a.Xor(f).GtU8(b.Xor(f))
}

// LtS8 implements i8x16.lt_s.
func (a V128) LtS8(b V128) V128 { return b.GtS8(a) }

func minU(x, y byte) byte {
	if x < y {
		return x
	}
	return y
}

func mapBytes(a, b uint64, f func(x, y byte) byte) uint64 {
	var out uint64
	for i := 0; i < 64; i += 8 {
		out |= uint64(f(byte(a>>i), byte(b>>i))) << i
	}
	return out
}

// MinU8 implements i8x16.min_u.
func (a V128) MinU8(b V128) V128 {
	return V128{mapBytes(a.Lo, b.Lo, minU), mapBytes(a.Hi, b.Hi, minU)}
}

// Bitmask8 gathers the high bit of each byte.
func (a V128) Bitmask8() uint32 {
	const magic = 0x0102040810204080
	lo := (((a.Lo & high) >> 7) * magic) >> 56
	hi := (((a.Hi & high) >> 7) * magic) >> 56
	return uint32(lo | hi<<8)
}

// AllTrue8 reports whether every byte is nonzero.
func (a V128) AllTrue8() bool { return zeroBytes(a.Lo)|zeroBytes(a.Hi) == 0 }

// Bytes16 builds a vector from 16 bytes.
func Bytes16(b [16]byte) V128 {
	return V128{binary.LittleEndian.Uint64(b[:8]), binary.LittleEndian.Uint64(b[8:])}
}
