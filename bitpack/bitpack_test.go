package bitpack

import "testing"

func TestWidthBoundaries(t *testing.T) {
	widths := []int{1, 7, 8, 63, 64}
	for _, w := range widths {
		mask, err := Mask(w)
		if err != nil {
			t.Fatalf("width %d: %v", w, err)
		}
		// Counts deliberately not multiples of 64 to exercise padding.
		for _, n := range []int{1, 3, 7, 63, 65, 100, 257} {
			vals := make([]uint64, n)
			for i := range vals {
				vals[i] = mask // every legal bit set
				if i%2 == 0 {
					vals[i] = uint64(i + 1) & mask
				}
			}
			buf := make([]byte, ByteLen(n, w))
			if err := Pack(buf, vals, w); err != nil {
				t.Fatalf("pack w=%d n=%d: %v", w, n, err)
			}
			got, err := Unpack(buf, n, w)
			if err != nil {
				t.Fatalf("unpack w=%d n=%d: %v", w, n, err)
			}
			if len(got) != n {
				t.Fatalf("w=%d n=%d: got %d values", w, n, len(got))
			}
			for i := range got {
				if got[i] != vals[i] {
					t.Fatalf("w=%d n=%d i=%d: got %d want %d", w, n, i, got[i], vals[i])
				}
			}
		}
	}
}

func TestWidth64Extremes(t *testing.T) {
	vals := []uint64{0, 1, ^uint64(0), ^uint64(0) - 1, 1 << 63}
	buf := make([]byte, ByteLen(len(vals), 64))
	if err := Pack(buf, vals, 64); err != nil {
		t.Fatal(err)
}
	got, err := Unpack(buf, len(vals), 64)
	if err != nil {
		t.Fatal(err)
	}
	for i := range got {
		if got[i] != vals[i] {
			t.Fatalf("i=%d got %#x want %#x", i, got[i], vals[i])
		}
	}
}

func TestPaddingBits(t *testing.T) {
	// 3 values of width 3 occupy 9 bits; the 7 padding bits must not become
	// a fourth value.
	buf := make([]byte, 2)
	if err := Pack(buf, []uint64{7, 0, 5}, 3); err != nil {
		t.Fatal(err)
	}
	got, err := Unpack(buf, 3, 3)
	if err != nil || len(got) != 3 || got[0] != 7 || got[1] != 0 || got[2] != 5 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestErrors(t *testing.T) {
	if _, err := Mask(0); err != ErrWidth {
		t.Fatalf("width 0: %v", err)
	}
	if _, err := Mask(65); err != ErrWidth {
		t.Fatalf("width 65: %v", err)
	}
	if err := Pack(make([]byte, 0), []uint64{1}, 8); err != ErrShort {
		t.Fatalf("short dst: %v", err)
	}
	if _, err := Unpack([]byte{0}, 2, 8); err != ErrShort {
		t.Fatalf("short src: %v", err)
	}
}
