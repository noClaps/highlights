package wasmrt

import (
	"math/rand"
	"testing"
)

func toBytes(v V128) (b [16]byte) {
	StV(b[:], v)
	return
}

func TestVectorOpsMatchLaneDefinitions(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for n := 0; n < 20000; n++ {
		var a, b [16]byte
		for i := range a {
			a[i] = byte(r.Intn(256))
			b[i] = byte(r.Intn(256))
			if r.Intn(4) == 0 {
				b[i] = a[i]
			}
		}
		va, vb := Bytes16(a), Bytes16(b)
		eq, sub, add := toBytes(Eq8(va, vb)), toBytes(Sub8(va, vb)), toBytes(Add8(va, vb))
		le, ge, lt, gt := toBytes(LeU8(va, vb)), toBytes(GeU8(va, vb)), toBytes(LtU8(va, vb)), toBytes(GtU8(va, vb))
		gts, lts := toBytes(GtS8(va, vb)), toBytes(LtS8(va, vb))
		lane := func(ok bool) byte {
			if ok {
				return 0xff
			}
			return 0
		}
		var mask uint32
		for i := range a {
			want := byte(0)
			if a[i] == b[i] {
				want = 0xff
			}
			if eq[i] != want {
				t.Fatalf("eq lane %d of %v %v", i, a, b)
			}
			if sub[i] != a[i]-b[i] || add[i] != a[i]+b[i] {
				t.Fatalf("add/sub lane %d of %v %v", i, a, b)
			}
			if le[i] != lane(a[i] <= b[i]) || ge[i] != lane(a[i] >= b[i]) || lt[i] != lane(a[i] < b[i]) || gt[i] != lane(a[i] > b[i]) {
				t.Fatalf("unsigned compare lane %d of %v %v", i, a, b)
			}
			if gts[i] != lane(int8(a[i]) > int8(b[i])) || lts[i] != lane(int8(a[i]) < int8(b[i])) {
				t.Fatalf("signed compare lane %d of %v %v", i, a, b)
			}
			if a[i]&0x80 != 0 {
				mask |= 1 << i
			}
		}
		if got := Bitmask8(va); got != mask {
			t.Fatalf("bitmask %v = %x, want %x", a, got, mask)
		}
	}
}
