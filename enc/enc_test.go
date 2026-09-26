package enc

import (
	"math"
	"math/rand"
	"testing"
)

func TestEncodeUintVectors(t *testing.T) {
	cases := []struct {
		v uint64
		b []byte
	}{
		{0, []byte{0x00}},
		{1, []byte{0x01}},
		{127, []byte{0x7f}},
		{128, []byte{0x80, 0x01}},
		{300, []byte{0xac, 0x02}},
		{16383, []byte{0xff, 0x7f}},
		{16384, []byte{0x80, 0x80, 0x01}},
		{2097151, []byte{0xff, 0xff, 0x7f}},
	}
	for _, c := range cases {
		if got := EncodeUint(c.v); !bytesEqual(got, c.b) {
			t.Errorf("EncodeUint(%d) = % x, want % x", c.v, got, c.b)
		}
	}
}

func TestRoundtrip(t *testing.T) {
	us := []uint64{0, 1, 63, 64, 127, 128, math.MaxUint64, math.MaxInt64,
		1 << 35, 1<<63 - 1, 1 << 63}
	for i := 0; i < 2000; i++ {
		us = append(us, rand.Uint64())
	}
	for _, v := range us {
		b := EncodeUint(v)
		got, n, err := DecodeUint(b)
		if err != nil || got != v || n != len(b) {
			t.Fatalf("uint roundtrip %d: got %d n %d err %v", v, got, n, err)
		}
	}
	ss := []int64{0, 1, -1, 63, 64, -64, -65, 127, -127, 128, -128,
		math.MaxInt64, math.MinInt64, math.MaxInt32, math.MinInt32}
	for i := 0; i < 2000; i++ {
		ss = append(ss, int64(rand.Uint64()))
	}
	for _, v := range ss {
		b := EncodeInt(v)
		got, n, err := DecodeInt(b)
		if err != nil || got != v || n != len(b) {
			t.Fatalf("int roundtrip %d: got %d n %d err %v (bytes % x)", v, got, n, err, b)
		}
	}
}

func TestNaiveReference(t *testing.T) {
	// Textbook unsigned encoder: plain 7-bit groups with continuation
	// bits, no canonical/minimality check.
	naive := func(v uint64) []byte {
		var out []byte
		for v >= 0x80 {
			out = append(out, byte(v&0x7f)|0x80)
			v >>= 7
		}
		return append(out, byte(v))
	}
	vals := []uint64{0, 1, 127, 128, 300, 16383, 16384, 2097151,
		math.MaxUint32, math.MaxInt64, math.MaxUint64}
	for i := 0; i < 1000; i++ {
		vals = append(vals, rand.Uint64())
	}
	for _, v := range vals {
		if got := EncodeUint(v); !bytesEqual(got, naive(v)) {
			t.Fatalf("mismatch at %d: % x vs % x", v, got, naive(v))
		}
	}
}

func TestCanonical(t *testing.T) {
	badU := [][]byte{{0x80, 0x00}, {0xff, 0xff, 0x00}, {0x80, 0x80, 0x00}}
	for _, b := range badU {
		if _, n, err := DecodeUint(b); err != ErrNonCanonical || n != 0 {
			t.Errorf("DecodeUint(% x) = err %v n %d, want ErrNonCanonical", b, err, n)
		}
	}
	badI := [][]byte{{0xff, 0x7f}, {0xc0, 0xff, 0x7f}, {0x80, 0x00}}
	for _, b := range badI {
		if _, n, err := DecodeInt(b); err != ErrNonCanonical || n != 0 {
			t.Errorf("DecodeInt(% x) = err %v n %d, want ErrNonCanonical", b, err, n)
		}
	}
	goodI := [][]byte{{0x40}, {0xc0, 0x00}, {0xbf, 0x7f}, {0xff, 0xbf, 0x7f}}
	wantI := []int64{-64, 64, -65, -8193}
	for j, b := range goodI {
		v, _, err := DecodeInt(b)
		if err != nil || v != wantI[j] {
			t.Errorf("DecodeInt(% x) = %d, %v; want %d", b, v, err, wantI[j])
		}
	}
}

func TestOverflow(t *testing.T) {
	badU := [][]byte{
		{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80},
		{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x02},
	}
	for _, b := range badU {
		if _, _, err := DecodeUint(b); err != ErrOverflow {
			t.Errorf("DecodeUint(% x): want ErrOverflow, got %v", b, err)
		}
	}
	badI := [][]byte{
		{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x01},
		{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7e},
	}
	for _, b := range badI {
		if _, _, err := DecodeInt(b); err != ErrOverflow {
			t.Errorf("DecodeInt(% x): want ErrOverflow, got %v", b, err)
		}
	}
	if ErrEmpty == ErrOverflow || ErrEmpty == ErrNonCanonical || ErrOverflow == ErrNonCanonical {
		t.Fatal("sentinel errors must be distinct")
	}
}

func TestEmpty(t *testing.T) {
	if _, n, err := DecodeUint(nil); err != ErrEmpty || n != 0 {
		t.Fatalf("DecodeUint(nil) = n %d err %v, want ErrEmpty", n, err)
	}
	if _, n, err := DecodeInt([]byte{0x80}); err != ErrEmpty || n != 0 {
		t.Fatalf("truncated signed: n %d err %v, want ErrEmpty", n, err)
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
