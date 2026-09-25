package hpack

import "testing"

// Packing m codewords and locating the start of the m-th one must scan a
// number of bits bounded by a small constant independent of m: the start
// is computed directly as bit offset 7*i, never walked from the head.
func TestLocateScansConstant(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		ns := make([]int, m)
		for i := range ns {
			ns[i] = i % 16
		}
		b := Pack(ns)
		u := &Unpacker{}
		got := u.Unpack(b, m)
		if got[m-1] != (m-1)%16 {
			t.Fatalf("m=%d: last nibble = %d, want %d", m, got[m-1], (m-1)%16)
		}
		if u.locateScans > 8 {
			t.Errorf("m=%d: locating last codeword scanned %d bits, want <= 8", m, u.locateScans)
		}
	}
}
