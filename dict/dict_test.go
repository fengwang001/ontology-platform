package dict

import (
	"math"
	"testing"
)

func TestIntRoundTrip(t *testing.T) {
	b := NewIntBuilder()
	in := []int64{0, -1, math.MaxInt64, math.MinInt64, 0, 42, -1, math.MaxInt64}
	codes := make([]int, len(in))
	for i, v := range in {
		codes[i] = b.Add(v)
	}
	if b.Cardinal() != 5 {
		t.Fatalf("cardinality %d", b.Cardinal())
	}
	tbl := NewIntTable(b.Values())
	for i, c := range codes {
		v, ok := tbl.Lookup(c)
		if !ok || v != in[i] {
			t.Fatalf("idx %d code %d -> %d,%v want %d", i, c, v, ok, in[i])
		}
	}
	if c, ok := b.CodeOf(123456); ok || c != 0 {
		t.Fatal("missing value reported present")
	}
	if _, ok := tbl.Lookup(b.Cardinal()); ok {
		t.Fatal("out-of-range code accepted")
	}
	if Width(b.Cardinal()) != 3 {
		t.Fatalf("width for 5 codes = %d, want 3", Width(b.Cardinal()))
	}
}

func TestBytesRoundTrip(t *testing.T) {
	b := NewBytesBuilder()
	in := [][]byte{[]byte(""), []byte("a"), []byte("bb"), []byte(""), []byte("a"), []byte("café")}
	codes := make([]int, len(in))
	for i, v := range in {
		codes[i] = b.Add(v)
	}
	if b.Cardinal() != 4 {
		t.Fatalf("cardinality %d", b.Cardinal())
	}
	tbl := NewBytesTable(b.Values())
	for i, c := range codes {
		v, ok := tbl.Lookup(c)
		if !ok || string(v) != string(in[i]) {
			t.Fatalf("idx %d code %d -> %q,%v want %q", i, c, v, ok, in[i])
		}
	}
	// Empty string is a real member distinct from absence.
	if c, ok := b.CodeOf([]byte("")); !ok || c != 0 {
		t.Fatalf("empty string code = %d,%v", c, ok)
	}
}

func TestWidthBoundaries(t *testing.T) {
	cases := map[int]uint{0: 0, 1: 0, 2: 1, 127: 7, 128: 7, 256: 8}
	for card, want := range cases {
		if got := Width(card); got != want {
			t.Fatalf("Width(%d) = %d, want %d", card, got, want)
		}
	}
}
