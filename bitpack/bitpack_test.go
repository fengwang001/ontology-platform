package bitpack

import (
	"math/rand/v2"
	"testing"
)

func TestWidthBoundaries(t *testing.T) {
	for _, w := range []int{1, 7, 8, 63, 64} {
		vals := []uint64{0, maxVal(w), maxVal(w) / 2}
		data, err := Pack(vals, w)
		if err != nil {
			t.Fatalf("width %d pack: %v", w, err)
		}
		got, err := Unpack(data, len(vals), w)
		if err != nil {
			t.Fatalf("width %d unpack: %v", w, err)
		}
		for i := range vals {
			if got[i] != vals[i] {
				t.Fatalf("width %d idx %d: got %d want %d", w, i, got[i], vals[i])
			}
		}
	}
}

func TestCrossWordRoundTrip(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for w := 1; w <= 64; w++ {
		for _, n := range []int{1, 2, 7, 8, 9, 63, 64, 65, 127, 130, 255} {
			vals := make([]uint64, n)
			for i := range vals {
				vals[i] = r.Uint64() & maxVal(w)
			}
			data, err := Pack(vals, w)
			if err != nil {
				t.Fatalf("w=%d n=%d pack: %v", w, n, err)
			}
			if len(data) != PackedLen(n, w) {
				t.Fatalf("w=%d n=%d packed len %d want %d", w, n, len(data), PackedLen(n, w))
			}
			got, err := Unpack(data, n, w)
			if err != nil {
				t.Fatalf("w=%d n=%d unpack: %v", w, n, err)
			}
			for i := range vals {
				if got[i] != vals[i] {
					t.Fatalf("w=%d n=%d idx %d: %d != %d", w, n, i, got[i], vals[i])
				}
			}
		}
	}
}

func TestPaddingBitsZero(t *testing.T) {
	data, _ := Pack([]uint64{1, 1, 1}, 1)
	for _, b := range data {
		if b&0xF8 != 0 {
			t.Fatalf("padding bits non-zero: %08b", b)
		}
	}
}

func TestInvalidWidthAndShort(t *testing.T) {
	if _, err := Pack(nil, 0); err != ErrWidth {
		t.Fatalf("width 0: %v", err)
	}
	if _, err := Pack(nil, 65); err != ErrWidth {
		t.Fatalf("width 65: %v", err)
	}
	data, _ := Pack([]uint64{255}, 8)
	if _, err := Unpack(data, 1, 8); err != nil {
		t.Fatal(err)
	}
	if _, err := Unpack(data[:len(data)-1], 1, 8); err != ErrShort {
		t.Fatal("truncated payload must be ErrShort")
	}
	if _, err := Pack([]uint64{2}, 1); err != ErrValue {
		t.Fatal("overflow value must be ErrValue")
	}
}

func maxVal(w int) uint64 {
	if w == 64 {
		return ^uint64(0)
	}
	return uint64(1)<<w - 1
}
