package hpack

import "testing"

// TestLocateScanBound pins the O(1) locate guarantee: the number of bits
// scanned to position the start of the m-th codeword must stay below a
// small constant independent of m (direct 7*i offset, no walk from head).
func TestLocateScanBound(t *testing.T) {
	const bound = 8 // small constant, independent of m
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		words := make([]uint8, m)
		for i := range words {
			words[i] = uint8(i%16) * 7 // arbitrary 7-bit values
		}
		stream := Pack(words)
		u := new(Unpacker)
		got, err := u.Unpack(stream, m)
		if err != nil {
			t.Fatalf("m=%d: %v", m, err)
		}
		if u.scanned > bound {
			t.Fatalf("m=%d: scanned %d bits to locate codeword, bound %d", m, u.scanned, bound)
		}
		for i := range got {
			if got[i] != words[i]&0x7f {
				t.Fatalf("m=%d: codeword %d = %07b, want %07b", m, i, got[i], words[i]&0x7f)
			}
		}
	}
}

// TestPackUnpackRoundTrip is a table-driven round trip over sizes whose
// bit counts fall on both sides of byte boundaries.
func TestPackUnpackRoundTrip(t *testing.T) {
	for _, m := range []int{0, 1, 2, 7, 8, 9, 100} {
		words := make([]uint8, m)
		for i := range words {
			words[i] = uint8((i*5 + 3) % 128)
		}
		got, err := Unpack(Pack(words), m)
		if err != nil {
			t.Fatalf("m=%d: %v", m, err)
		}
		for i := range got {
			if got[i] != words[i]&0x7f {
				t.Fatalf("m=%d: codeword %d mismatch", m, i)
			}
		}
	}
}
