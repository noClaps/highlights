// Package wasmrt holds the runtime helpers that generated Wasm-to-Go code
// calls: little-endian memory access, booleans, and 128-bit byte vectors.
package wasmrt

import (
	"encoding/binary"
	"math/bits"
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

func Sel32(c bool, a, b uint32) uint32 {
	if c {
		return a
	}
	return b
}

func Sel64(c bool, a, b uint64) uint64 {
	if c {
		return a
	}
	return b
}

func SelV(c bool, a, b V128) V128 {
	if c {
		return a
	}
	return b
}

func Ld8(mem []byte, i int) uint32  { return uint32(mem[i]) }
func Ld16(mem []byte, i int) uint32 { return uint32(binary.LittleEndian.Uint16(mem[i:])) }
func Ld16s(mem []byte, i int) uint32 {
	return uint32(int32(int16(binary.LittleEndian.Uint16(mem[i:]))))
}
func Ld8s(mem []byte, i int) uint32 { return uint32(int32(int8(mem[i]))) }
func Ld32(mem []byte, i int) uint32 { return binary.LittleEndian.Uint32(mem[i:]) }
func Ld64(mem []byte, i int) uint64 { return binary.LittleEndian.Uint64(mem[i:]) }

func Ld64_8(mem []byte, i int) uint64  { return uint64(mem[i]) }
func Ld64_16(mem []byte, i int) uint64 { return uint64(binary.LittleEndian.Uint16(mem[i:])) }
func Ld64_32(mem []byte, i int) uint64 { return uint64(binary.LittleEndian.Uint32(mem[i:])) }

func St8(mem []byte, i int, v uint32)  { mem[i] = byte(v) }
func St16(mem []byte, i int, v uint32) { binary.LittleEndian.PutUint16(mem[i:], uint16(v)) }
func St32(mem []byte, i int, v uint32) { binary.LittleEndian.PutUint32(mem[i:], v) }
func St64(mem []byte, i int, v uint64) { binary.LittleEndian.PutUint64(mem[i:], v) }

func St64_8(mem []byte, i int, v uint64)  { mem[i] = byte(v) }
func St64_16(mem []byte, i int, v uint64) { binary.LittleEndian.PutUint16(mem[i:], uint16(v)) }
func St64_32(mem []byte, i int, v uint64) { binary.LittleEndian.PutUint32(mem[i:], uint32(v)) }

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

func Clz32(x uint32) uint32    { return uint32(bits.LeadingZeros32(x)) }
func Ctz32(x uint32) uint32    { return uint32(bits.TrailingZeros32(x)) }
func Popcnt32(x uint32) uint32 { return uint32(bits.OnesCount32(x)) }
func Clz64(x uint64) uint64    { return uint64(bits.LeadingZeros64(x)) }
func Ctz64(x uint64) uint64    { return uint64(bits.TrailingZeros64(x)) }
func Popcnt64(x uint64) uint64 { return uint64(bits.OnesCount64(x)) }
func Rotl32(x, k uint32) uint32 { return bits.RotateLeft32(x, int(k&31)) }
func Rotr32(x, k uint32) uint32 { return bits.RotateLeft32(x, -int(k&31)) }
func Rotl64(x, k uint64) uint64 { return bits.RotateLeft64(x, int(k&63)) }
func Rotr64(x, k uint64) uint64 { return bits.RotateLeft64(x, -int(k&63)) }

// V128 is a 128-bit vector stored as two little-endian 64-bit lanes.
type V128 struct{ Lo, Hi uint64 }

const (
	ones = 0x0101010101010101
	high = 0x8080808080808080
	low7 = 0x7f7f7f7f7f7f7f7f
)

func LdV(mem []byte, i int) V128 {
	_ = mem[i+15]
	return V128{binary.LittleEndian.Uint64(mem[i:]), binary.LittleEndian.Uint64(mem[i+8:])}
}

func StV(mem []byte, i int, v V128) {
	_ = mem[i+15]
	binary.LittleEndian.PutUint64(mem[i:], v.Lo)
	binary.LittleEndian.PutUint64(mem[i+8:], v.Hi)
}

func Splat8(x uint32) V128 {
	v := uint64(byte(x)) * ones
	return V128{v, v}
}

func Or(a, b V128) V128     { return V128{a.Lo | b.Lo, a.Hi | b.Hi} }
func And(a, b V128) V128    { return V128{a.Lo & b.Lo, a.Hi & b.Hi} }
func Xor(a, b V128) V128    { return V128{a.Lo ^ b.Lo, a.Hi ^ b.Hi} }
func AndNot(a, b V128) V128 { return V128{a.Lo &^ b.Lo, a.Hi &^ b.Hi} }
func Not(a V128) V128       { return V128{^a.Lo, ^a.Hi} }
func AnyTrue(a V128) bool   { return a.Lo|a.Hi != 0 }

// zeroBytes sets 0xff in each byte of x that is zero.
func zeroBytes(x uint64) uint64 {
	t := ((x & low7) + low7) | x
	return ((^t & high) >> 7) * 0xff
}

func Eq8(a, b V128) V128 { return V128{zeroBytes(a.Lo ^ b.Lo), zeroBytes(a.Hi ^ b.Hi)} }
func Ne8(a, b V128) V128 { return Not(Eq8(a, b)) }

func sub8(a, b uint64) uint64 { return ((a | high) - (b &^ high)) ^ ((a ^ ^b) & high) }
func Sub8(a, b V128) V128     { return V128{sub8(a.Lo, b.Lo), sub8(a.Hi, b.Hi)} }

func add8(a, b uint64) uint64 { return ((a &^ high) + (b &^ high)) ^ ((a ^ b) & high) }
func Add8(a, b V128) V128     { return V128{add8(a.Lo, b.Lo), add8(a.Hi, b.Hi)} }

func lanes(f func(x, y byte) bool, a, b uint64) uint64 {
	var out uint64
	for i := 0; i < 64; i += 8 {
		if f(byte(a>>i), byte(b>>i)) {
			out |= 0xff << i
		}
	}
	return out
}

func cmp8(a, b V128, f func(x, y byte) bool) V128 {
	return V128{lanes(f, a.Lo, b.Lo), lanes(f, a.Hi, b.Hi)}
}

func LeU8(a, b V128) V128 { return cmp8(a, b, func(x, y byte) bool { return x <= y }) }
func GeU8(a, b V128) V128 { return cmp8(a, b, func(x, y byte) bool { return x >= y }) }
func LtU8(a, b V128) V128 { return cmp8(a, b, func(x, y byte) bool { return x < y }) }
func GtU8(a, b V128) V128 { return cmp8(a, b, func(x, y byte) bool { return x > y }) }
func GtS8(a, b V128) V128 {
	return cmp8(a, b, func(x, y byte) bool { return int8(x) > int8(y) })
}
func LtS8(a, b V128) V128 {
	return cmp8(a, b, func(x, y byte) bool { return int8(x) < int8(y) })
}

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

func MinU8(a, b V128) V128 {
	return V128{mapBytes(a.Lo, b.Lo, minU), mapBytes(a.Hi, b.Hi, minU)}
}

// Bitmask8 gathers the high bit of each byte.
func Bitmask8(a V128) uint32 {
	const magic = 0x0102040810204080
	lo := (((a.Lo & high) >> 7) * magic) >> 56
	hi := (((a.Hi & high) >> 7) * magic) >> 56
	return uint32(lo | hi<<8)
}

// AllTrue8 reports whether every byte is nonzero.
func AllTrue8(a V128) bool { return zeroBytes(a.Lo)|zeroBytes(a.Hi) == 0 }

// Bytes16 builds a vector from 16 bytes.
func Bytes16(b [16]byte) V128 {
	return V128{binary.LittleEndian.Uint64(b[:8]), binary.LittleEndian.Uint64(b[8:])}
}
