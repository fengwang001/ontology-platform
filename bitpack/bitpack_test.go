package bitpack

import "testing"

func TestPackRoundTrip(t *testing.T) {
	widths := []uint{1, 7, 8, 63, 64}
	sizes := []int{0, 1, 2, 7, 8, 9, 63, 64, 65, 100, 127, 128, 129}
	for _, width := range widths {
		for _, n := range sizes {
			vals := make([]uint64, n)
			for i := range vals {
				// Pattern that exercises high and low bits at every position.
				vals[i] = uint64(i)*0x9E3779B97F4A7C15 + (uint64(1)<<width)-1
				vals[i] &= (uint64(1) << width) - 1
			}
			buf, err := Pack(vals, width)
			if err != nil {
				t.Fatalf("width=%d n=%d pack: %v", width, n, err)
			}
			if len(buf) != PackedLen(n, width) {
				t.Fatalf("width=%d n=%d len=%d want %d", width, n, len(buf), PackedLen(n, width))
			}
			got, err := Unpack(buf, n, width)
			if err != nil {
				t.Fatalf("width=%d n=%d unpack: %v", width, n, err)
			}
			if len(got) != n { // never read past the declared value count
				t.Fatalf("width=%d n=%d produced %d values", width, n, len(got))
			}
			for i := range vals {
				if got[i] != vals[i] {
					t.Fatalf("width=%d n=%d idx=%d got=%d want=%d", width, n, i, got[i], vals[i])
				}
			}
			// Truncating even one byte must be a decidable error.
			if len(buf) > 0 {
				if _, err := Unpack(buf[:len(buf)-1], n, width); err == nil {
					t.Fatalf("width=%d n=%d expected truncation error", width, n)
				}
			}
		}
	}

	if _, err := Pack(nil, 0); err != ErrWidth {
		t.Fatalf("width 0: %v", err)
	}
	if _, err := Pack(nil, 65); err != ErrWidth {
		t.Fatalf("width 65: %v", err)
	}
}

func TestZigZagAndWidth(t *testing.T) {
	cases := []int64{0, 1, -1, 2, -2, 1<<31 - 1, -1 << 31, 1<<62 - 1, -1 << 62, 1<<62, 1<<63 - 1, -1 << 63}
	for _, v := range cases {
		if UnZigZag(ZigZag(v)) != v {
			t.Fatalf("zigzag not invertible at %d", v)
		}
	}
	want := map[uint64]uint{0: 1, 1: 1, 2: 2, 255: 8, 256: 9, 1 << 63: 64}
	for v, w := range want {
		if UWidth(v) != w {
			t.Fatalf("UWidth(%d)=%d want %d", v, UWidth(v), w)
		}
	}
}
