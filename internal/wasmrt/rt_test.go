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
		eq, sub, add := toBytes(va.Eq8(vb)), toBytes(va.Sub8(vb)), toBytes(va.Add8(vb))
		le, ge, lt, gt := toBytes(va.LeU8(vb)), toBytes(va.GeU8(vb)), toBytes(va.LtU8(vb)), toBytes(va.GtU8(vb))
		gts, lts := toBytes(va.GtS8(vb)), toBytes(va.LtS8(vb))
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
		if got := va.Bitmask8(); got != mask {
			t.Fatalf("bitmask %v = %x, want %x", a, got, mask)
		}
	}
}
