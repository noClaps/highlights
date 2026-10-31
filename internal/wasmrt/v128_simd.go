//go:build goexperiment.simd && arm64

package wasmrt

import "simd/archsimd"

// V128 is a 128-bit vector of 16 byte lanes in a NEON register. This
// version is built with GOEXPERIMENT=simd on arm64; elsewhere V128 is two
// 64-bit words.
type V128 struct{ v archsimd.Uint8x16 }

// LdV implements v128.load from the first 16 bytes of b.
func LdV(b []byte) V128 { return V128{archsimd.LoadUint8x16(b[:16])} }

// StV implements v128.store to the first 16 bytes of b.
func StV(b []byte, v V128) { v.v.Store(b[:16]) }

// Splat8 implements i8x16.splat.
func Splat8(x uint32) V128 { return V128{archsimd.BroadcastUint8x16(uint8(x))} }

// Bytes16 builds a vector from 16 bytes.
func Bytes16(b [16]byte) V128 { return V128{archsimd.LoadUint8x16Array(&b)} }

// lanes turns a comparison into 0xff in each lane where it holds.
func lanes(m archsimd.Mask8x16) V128 { return V128{m.ToInt8x16().ToBits()} }

// Or implements v128.or.
func (a V128) Or(b V128) V128 { return V128{a.v.Or(b.v)} }

// And implements v128.and.
func (a V128) And(b V128) V128 { return V128{a.v.And(b.v)} }

// Xor implements v128.xor.
func (a V128) Xor(b V128) V128 { return V128{a.v.Xor(b.v)} }

// AndNot implements v128.andnot.
func (a V128) AndNot(b V128) V128 { return V128{a.v.AndNot(b.v)} }

// Not implements v128.not.
func (a V128) Not() V128 { return V128{a.v.Not()} }

// AnyTrue implements v128.any_true: some byte is nonzero when the smallest
// byte of the complement is below 0xff.
func (a V128) AnyTrue() bool { return a.v.Not().ReduceMin() != 0xff }

// Eq8 implements i8x16.eq.
func (a V128) Eq8(b V128) V128 { return lanes(a.v.Equal(b.v)) }

// EqHi8 is Eq8 for a result that only Bitmask8, AnyTrue, Or and And read;
// a full lane costs nothing more here.
func (a V128) EqHi8(b V128) V128 { return a.Eq8(b) }

// Ne8 implements i8x16.ne.
func (a V128) Ne8(b V128) V128 { return lanes(a.v.NotEqual(b.v)) }

// Sub8 implements i8x16.sub.
func (a V128) Sub8(b V128) V128 { return V128{a.v.Sub(b.v)} }

// Add8 implements i8x16.add.
func (a V128) Add8(b V128) V128 { return V128{a.v.Add(b.v)} }

// GeU8 implements i8x16.ge_u.
func (a V128) GeU8(b V128) V128 { return lanes(a.v.GreaterEqual(b.v)) }

// GeHi8 is GeU8, as EqHi8 is Eq8.
func (a V128) GeHi8(b V128) V128 { return a.GeU8(b) }

// LeU8 implements i8x16.le_u.
func (a V128) LeU8(b V128) V128 { return b.GeU8(a) }

// LtU8 implements i8x16.lt_u.
func (a V128) LtU8(b V128) V128 { return lanes(a.v.Less(b.v)) }

// GtU8 implements i8x16.gt_u.
func (a V128) GtU8(b V128) V128 { return lanes(a.v.Greater(b.v)) }

// GtS8 implements i8x16.gt_s.
func (a V128) GtS8(b V128) V128 { return lanes(a.v.BitsToInt8().Greater(b.v.BitsToInt8())) }

// LtS8 implements i8x16.lt_s.
func (a V128) LtS8(b V128) V128 { return b.GtS8(a) }

// MinU8 implements i8x16.min_u.
func (a V128) MinU8(b V128) V128 { return V128{a.v.Min(b.v)} }

// Bitmask8 gathers the high bit of each byte. NEON has no instruction for
// it, so the two halves move to general registers and gather as the
// portable V128 does.
func (a V128) Bitmask8() uint32 {
	w := a.v.ReshapeToUint64s()
	const high, magic = 0x8080808080808080, 0x0102040810204080
	lo := (((w.GetElem(0) & high) >> 7) * magic) >> 56
	hi := (((w.GetElem(1) & high) >> 7) * magic) >> 56
	return uint32(lo | hi<<8)
}

// AllTrue8 reports whether every byte is nonzero.
func (a V128) AllTrue8() bool { return a.v.ReduceMin() != 0 }
