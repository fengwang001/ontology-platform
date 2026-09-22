package bitpack

import (
	"math"
	"testing"
)

func TestBoundaryWidths(t *testing.T) {
	widths := []uint{1, 7, 8, 63, 64}
	counts := []int{0, 1, 2, 7, 8, 9, 63, 64, 65, 100}
	for _, w := range widths {
		for _, n := range counts {
			vals := make([]uint64, n)
			for i := range vals {
				switch {
				case w == 64 && i%3 == 0:
					vals[i] = math.MaxInt64
				case w == 64 && i%3 == 1:
					vals[i] = math.MaxUint64
				case i%2 == 0:
					vals[i] = 1<<w - 1
				default:
					vals[i] = uint64(i) & (1<<w - 1)
				}
			}
			raw, err := Pack(vals, n, w)
			if err != nil {
				t.Fatalf("width %d count %d: pack: %v", w, n, err)
			}
			if got := PackedSize(n, w); got != len(raw) {
				t.Fatalf("width %d count %d: size %d != %d", w, n, len(raw), got)
			}
			out := make([]uint64, n)
			if err := Unpack(raw, n, w, out); err != nil {
				t.Fatalf("width %d count %d: unpack: %v", w, n, err)
			}
			for i := range vals {
				if out[i] != vals[i] {
					t.Fatalf("width %d count %d idx %d: %d != %d", w, n, i, out[i], vals[i])
				}
			}
		}
	}
}

func TestCrossWordBoundaries(t *testing.T) {
	// Width 3 forces values to straddle byte boundaries.
	src := []uint64{0, 1, 2, 3, 4, 5, 6, 7, 0, 7, 3, 4}
	raw, err := Pack(src, len(src), 3)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]uint64, len(src))
	if err := Unpack(raw, len(src), 3, out); err != nil {
		t.Fatal(err)
	}
	for i := range src {
		if out[i] != src[i] {
			t.Fatalf("idx %d: %d != %d", i, out[i], src[i])
		}
	}
}

func TestTruncationAndPaddingRejected(t *testing.T) {
	src := []uint64{1, 2, 3}
	raw, _ := Pack(src, 3, 3)
	out := make([]uint64, 3)
	if err := Unpack(raw[:len(raw)-1], 3, 3, out); err == nil {
		t.Fatal("truncated block accepted")
	}
	dirty := append([]byte(nil), raw...)
	dirty[len(dirty)-1] |= 0x80 // padding bits must be zero
	if err := Unpack(dirty, 3, 3, out); err == nil {
		t.Fatal("dirty padding accepted")
	}
}

func TestBadWidth(t *testing.T) {
	if _, err := Pack(nil, 1, 0); err != ErrWidth {
		t.Fatalf("width 0: %v", err)
	}
	if _, err := Pack(nil, 1, 65); err != ErrWidth {
		t.Fatalf("width 65: %v", err)
	}
	if err := Unpack(nil, 1, 0, make([]uint64, 1)); err != ErrWidth {
		t.Fatalf("unpack width 0: %v", err)
	}
}
