package bitpack

import (
	"math"
	"testing"
)

func TestPackRoundTrip(t *testing.T) {
	widths := []int{1, 7, 8, 63, 64}
	counts := []int{0, 1, 2, 7, 8, 9, 63, 64, 65, 100, 127, 128, 129, 1000}
	extremes := []int64{0, 1, -1, math.MaxInt64, math.MinInt64, 42, -42, 127, 128, -128, -129}
	for _, w := range widths {
		for _, n := range counts {
			vals := make([]uint64, n)
			for i := range vals {
				vals[i] = Zig(extremes[(i*7+w+n)%len(extremes)])
				// keep value representable in w bits
				if Width64(vals[i]) > w {
					vals[i] &= uint64(1)<<uint(w-1) - 1
				}
			}
			raw := Pack(vals, w)
			if len(raw) != PackedLen(n, w) {
				t.Fatalf("w=%d n=%d len=%d want %d", w, n, len(raw), PackedLen(n, w))
			}
			got, err := Unpack(raw, w, n)
			if err != nil {
				t.Fatalf("w=%d n=%d: %v", w, n, err)
			}
			for i := range got {
				if got[i] != vals[i] {
					t.Fatalf("w=%d n=%d i=%d got %d want %d", w, n, i, got[i], vals[i])
				}
			}
			// asking for more values than the payload supports must fail, padding
			// bits must never surface as extra values.
			if PackedLen(n+1, w) != len(raw) {
				if _, err := Unpack(raw, w, n+1); err == nil {
					t.Fatalf("w=%d n=%d expected over-read error", w, n)
				}
			}
			if len(raw) > 0 {
				if _, err := Unpack(raw[:len(raw)-1], w, n); err == nil {
					t.Fatalf("w=%d n=%d expected truncation error", w, n)
				}
			}
		}
	}
}

func TestZigZagOrderingAndExtremes(t *testing.T) {
	cases := []int64{0, -1, 1, math.MinInt64, math.MaxInt64, 1 << 62, -(1 << 62)}
	for _, v := range cases {
		if got := Unzig(Zig(v)); got != v {
			t.Fatalf("zigzag round trip %d -> %d", v, got)
		}
	}
	if Width64(Zig(math.MinInt64)) != 64 || Width64(Zig(math.MaxInt64)) != 64 {
		t.Fatal("extreme widths wrong")
	}
}

func TestBadWidth(t *testing.T) {
	for _, w := range []int{0, -1, 65, 100} {
		if _, err := Unpack(nil, w, 0); err != ErrBadWidth {
			t.Fatalf("w=%d err=%v", w, err)
		}
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("w=%d Pack should panic", w)
				}
			}()
			Pack([]uint64{1}, w)
		}()
	}
}
