package bitpack

import (
	"math/rand"
	"testing"
)

func roundTrip(t *testing.T, vals []uint64, width uint8) {
	t.Helper()
	packed := Pack(nil, vals, width)
	if len(packed) != PackedLen(len(vals), width) {
		t.Fatalf("packed len %d, want %d", len(packed), PackedLen(len(vals), width))
	}
	out, err := Unpack(packed, len(vals), width)
	if err != nil {
		t.Fatalf("unpack: %v", err)
	}
	if len(out) != len(vals) {
		t.Fatalf("decoded %d values, want %d", len(out), len(vals))
	}
	mask := ^uint64(0)
	if width < 64 {
		mask = 1<<width - 1
	}
	for i, v := range vals {
		if out[i] != v&mask {
			t.Fatalf("value %d: got %d, want %d", i, out[i], v&mask)
		}
	}
}

func TestBoundaryWidths(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for _, width := range []uint8{1, 7, 8, 63, 64} {
		for _, n := range []int{0, 1, 3, 64, 65, 129} {
			vals := make([]uint64, n)
			for i := range vals {
				vals[i] = rng.Uint64()
			}
			roundTrip(t, vals, width)
		}
	}
}

func TestExtremeValues(t *testing.T) {
	vals := []uint64{0, 1<<64 - 1, 1 << 63, 0xAAAAAAAAAAAAAAAA, 1}
	roundTrip(t, vals, 64)
	roundTrip(t, []uint64{0, 127, 128, 255}, 8)
	roundTrip(t, []uint64{0, 1, 1, 0, 1, 1, 1, 0, 1}, 1)
}

func TestNoExtraValuesFromPadding(t *testing.T) {
	// 5 values at width 7 = 35 bits = 5 bytes with 5 padding bits.
	vals := []uint64{1, 2, 3, 4, 5}
	packed := Pack(nil, vals, 7)
	if len(packed) != 5 {
		t.Fatalf("packed len %d, want 5", len(packed))
	}
	out, err := Unpack(packed, len(vals), 7)
	if err != nil || len(out) != 5 {
		t.Fatalf("unpack: %v, n=%d", err, len(out))
	}
	// Asking for more values than were packed must fail, not invent
	// values from padding bits.
	if _, err := Unpack(packed, 6, 7); err != ErrShortBuffer {
		t.Fatalf("want ErrShortBuffer, got %v", err)
	}
}

func TestBadWidth(t *testing.T) {
	if _, err := Unpack(nil, 0, 0); err != ErrBadWidth {
		t.Fatalf("want ErrBadWidth, got %v", err)
	}
	if _, err := Unpack(nil, 0, 65); err != ErrBadWidth {
		t.Fatalf("want ErrBadWidth, got %v", err)
	}
}

func TestMinWidth(t *testing.T) {
	cases := map[uint64]uint8{0: 1, 1: 1, 2: 2, 255: 8, 256: 9, 1<<64 - 1: 64}
	for max, want := range cases {
		if got := MinWidth(max); got != want {
			t.Fatalf("MinWidth(%d)=%d, want %d", max, got, want)
		}
	}
}
