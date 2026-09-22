package bitpack

import (
	"math/rand"
	"testing"
)

func TestRoundTripWidths(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for _, w := range []int{1, 2, 3, 7, 8, 9, 31, 32, 33, 63, 64} {
		for _, n := range []int{0, 1, 5, 63, 64, 65, 127, 1000} {
			vals := make([]uint64, n)
			mask := maskOf(w)
			for i := range vals {
				vals[i] = uint64(rng.Int63())<<1 | uint64(rng.Intn(2))
				vals[i] &= mask
			}
			packed, err := Encode(vals, w)
			if err != nil {
				t.Fatalf("w=%d n=%d encode: %v", w, n, err)
			}
			if len(packed) != PackedLen(w, n) {
				t.Fatalf("w=%d n=%d packed len=%d want %d", w, n, len(packed), PackedLen(w, n))
			}
			got, err := DecodeAll(packed, w, n)
			if err != nil {
				t.Fatalf("w=%d n=%d decode: %v", w, n, err)
			}
			for i := range vals {
				if got[i] != vals[i] {
					t.Fatalf("w=%d n=%d idx=%d got=%d want=%d", w, n, i, got[i], vals[i])
				}
			}
		}
	}
}

func TestBoundaryValues(t *testing.T) {
	// 极值：全 0、全 1、交替位，覆盖跨字边界错位风险。
	for _, w := range []int{1, 7, 8, 63, 64} {
		mask := maskOf(w)
		vals := []uint64{0, mask, mask - 1, 1, mask & 0xAAAAAAAAAAAAAAAA, mask & 0x5555555555555555}
		packed, err := Encode(vals, w)
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeAll(packed, w, len(vals))
		if err != nil {
			t.Fatal(err)
		}
		for i := range vals {
			if got[i] != vals[i] {
				t.Fatalf("w=%d idx=%d got=%x want=%x", w, i, got[i], vals[i])
			}
		}
	}
}

func TestTrailingPaddingNotRead(t *testing.T) {
	// n 不是 8 的整数倍时，末尾补位不得解出多余的值。
	vals := []uint64{1, 1, 1}
	packed, err := Encode(vals, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(packed) != 2 {
		t.Fatalf("len=%d want 2", len(packed))
	}
	got, err := DecodeAll(packed, 3, 3)
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range got {
		if v != 1 {
			t.Fatalf("idx=%d got=%d", i, v)
		}
	}
}

func TestShortBuffer(t *testing.T) {
	packed, err := Encode([]uint64{1, 2, 3, 4, 5}, 16)
	if err != nil {
		t.Fatal(err)
	}
	for cut := 0; cut < len(packed); cut++ {
		if _, err := DecodeAll(packed[:cut], 16, 5); err != ErrShortBuffer {
			t.Fatalf("cut=%d err=%v want ErrShortBuffer", cut, err)
		}
	}
}

func TestInvalidWidth(t *testing.T) {
	for _, w := range []int{0, -1, 65} {
		if _, err := Encode([]uint64{1}, w); err != ErrInvalidWidth {
			t.Fatalf("w=%d err=%v", w, err)
		}
		if _, err := DecodeAll(make([]byte, 8), w, 1); err != ErrInvalidWidth {
			t.Fatalf("w=%d err=%v", w, err)
		}
	}
}

func TestWidthFor(t *testing.T) {
	cases := map[uint64]int{0: 1, 1: 1, 2: 2, 255: 8, 256: 9, 1 << 63: 64, ^uint64(0): 64}
	for v, want := range cases {
		if got := WidthFor(v); got != want {
			t.Fatalf("WidthFor(%d)=%d want %d", v, got, want)
		}
	}
}
