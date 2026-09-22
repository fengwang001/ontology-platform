package bitpack

import (
	"math"
	"testing"
)

func TestWidthsRoundTrip(t *testing.T) {
	widths := []int{1, 2, 7, 8, 9, 15, 16, 31, 32, 33, 63, 64}
	counts := []int{1, 2, 7, 8, 9, 63, 64, 65, 100, 127, 128, 129}
	for _, w := range widths {
		for _, n := range counts {
			var max uint64
			if w == 64 {
				max = math.MaxUint64
			} else {
				max = (uint64(1) << uint(w)) - 1
			}
			vals := make([]uint64, n)
			for i := range vals {
				switch i % 5 {
				case 0:
					vals[i] = 0
				case 1:
					vals[i] = max
				case 2:
					vals[i] = 1
				case 3:
					vals[i] = max >> 1
				default:
					vals[i] = uint64(i * 7)
				}
				if vals[i] > max {
					vals[i] = max
				}
			}
			data, err := Pack(vals, w)
			if err != nil {
				t.Fatalf("w=%d n=%d pack: %v", w, n, err)
			}
			if len(data) != PackedLen(n, w) {
				t.Fatalf("w=%d n=%d len=%d want %d", w, n, len(data), PackedLen(n, w))
			}
			got, err := Unpack(data, w, n)
			if err != nil {
				t.Fatalf("w=%d n=%d unpack: %v", w, n, err)
			}
			for i := range got {
				if got[i] != vals[i] {
					t.Fatalf("w=%d n=%d i=%d got=%d want=%d", w, n, i, got[i], vals[i])
				}
			}
		}
	}
}

func TestCrossWordBoundaries(t *testing.T) {
	// A 7-bit pattern packed across many bytes: each successive value is
	// shifted by one bit position, exercising every cross-byte alignment.
	n := 200
	vals := make([]uint64, n)
	for i := range vals {
		vals[i] = uint64(0x7f>>uint(i%7)) | (uint64(i%3) << 5)
		if vals[i] > 0x7f {
			vals[i] &= 0x7f
		}
	}
	data, err := Pack(vals, 7)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Unpack(data, 7, n)
	if err != nil {
		t.Fatal(err)
	}
	for i := range got {
		if got[i] != vals[i] {
			t.Fatalf("i=%d got=%07b want=%07b", i, got[i], vals[i])
		}
	}
}

func TestTrailingPaddingYieldsNoExtras(t *testing.T) {
	for _, n := range []int{1, 3, 7, 9, 31, 33, 63, 65} {
		vals := make([]uint64, n)
		for i := range vals {
			vals[i] = 1
		}
		data, _ := Pack(vals, 1)
		if len(data) != (n+7)/8 {
			t.Fatalf("n=%d packed len %d", n, len(data))
		}
		got, err := Unpack(data, 1, n)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != n {
			t.Fatalf("n=%d got %d values", n, len(got))
		}
		// Padding bits set to 1 must not leak out: they are never read.
		for i := range data {
			data[i] = 0xff
		}
		got, _ = Unpack(data, 1, n)
		for _, v := range got {
			if v != 1 {
				t.Fatal("padding bits leaked into values")
			}
		}
	}
}

func TestWidth64Extremes(t *testing.T) {
	vals := []uint64{0, 1, math.MaxUint64, math.MaxUint64 - 1}
	data, err := Pack(vals, 64)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Unpack(data, 64, len(vals))
	if err != nil {
		t.Fatal(err)
	}
	for i := range vals {
		if got[i] != vals[i] {
			t.Fatalf("i=%d %d != %d", i, got[i], vals[i])
		}
	}
}

func TestErrors(t *testing.T) {
	if _, err := Pack(nil, 0); err != ErrWidth {
		t.Fatalf("width 0: %v", err)
	}
	if _, err := Pack(nil, 65); err != ErrWidth {
		t.Fatalf("width 65: %v", err)
	}
	if _, err := Pack([]uint64{2}, 1); err != ErrOverflow {
		t.Fatalf("overflow: %v", err)
	}
	if _, err := Unpack([]byte{0}, 8, 2); err != ErrShortData {
		t.Fatalf("short: %v", err)
	}
	if err := PackInto([]uint64{0}, 1, nil); err != ErrShortData {
		t.Fatalf("packinto short: %v", err)
	}
}

func TestSignedRoundTrip(t *testing.T) {
	vals := []int64{0, 1, -1, math.MaxInt64, math.MinInt64, 42, -42, 1 << 40, -(1 << 40)}
	data, err := PackSigned(vals, 64)
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnpackSigned(data, 64, len(vals))
	if err != nil {
		t.Fatal(err)
	}
	for i := range vals {
		if got[i] != vals[i] {
			t.Fatalf("i=%d %d != %d", i, got[i], vals[i])
		}
	}
	if ZigZag(-1) != 1 || UnZigZag(1) != -1 {
		t.Fatal("zigzag -1 mapping wrong")
	}
}
