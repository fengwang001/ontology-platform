package wire

import (
	"errors"
	"testing"
)

// TestLookupConstantComparisons proves field access is hash-indexed: the
// comparison counter must stay bounded by a small constant as m grows.
func TestLookupConstantComparisons(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		fs := make([]Field, m)
		schema := map[int]int{}
		for i := range fs {
			fs[i] = Field{Num: i + 1, Wire: WireVarint, U: uint64(i)}
			schema[i+1] = WireVarint
		}
		b, err := Encode(fs)
		if err != nil {
			t.Fatal(err)
		}
		dec, err := Decode(b, schema)
		if err != nil {
			t.Fatal(err)
		}
		f, ok := Lookup(dec, m) // fetch the LAST field
		if !ok || f.U != uint64(m-1) {
			t.Fatalf("m=%d: wrong value", m)
		}
		if got := lastLookupCmp.Load(); got > 2 {
			t.Fatalf("m=%d: comparisons=%d grows with m", m, got)
		}
	}
}

// TestFaultInjection pins each rejection to its own sentinel error and
// verifies the decoder stays usable afterwards.
func TestFaultInjection(t *testing.T) {
	schema := map[int]int{1: WireVarint, 2: WireLengthDelim}
	cases := []struct {
		name string
		in   []byte
		want error
	}{
		{"trunc-lengthdelim", []byte{0x12, 0x05, 0x41}, ErrTruncated},
		{"trunc-fixed32", []byte{0x1D, 0x01, 0x02}, ErrTruncated},
		{"trunc-fixed64", []byte{0x09, 0x01, 0x02, 0x03}, ErrTruncated},
		{"overflow-11B", []byte{0x08, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, ErrOverflow},
		{"overflow-10th-byte-2bits", []byte{0x08, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x02}, ErrOverflow},
		{"wiretype-3", []byte{0x0B, 0x00}, ErrBadWireType},
		{"wiretype-4", []byte{0x0C, 0x00}, ErrBadWireType},
		{"wiretype-6", []byte{0x0E, 0x00}, ErrBadWireType},
		{"wiretype-7", []byte{0x0F, 0x00}, ErrBadWireType},
		{"duplicate", []byte{0x08, 0x01, 0x08, 0x02}, ErrDuplicateField},
		{"fieldnum-zero", []byte{0x00, 0x00}, ErrFieldNumZero},
	}
	for _, c := range cases {
		m, err := Decode(c.in, schema)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: got err=%v want %v", c.name, err, c.want)
		}
		if m != nil {
			t.Errorf("%s: partial fields leaked", c.name)
		}
	}
	// Sentinels are pairwise distinct.
	all := []error{ErrTruncated, ErrOverflow, ErrBadWireType, ErrDuplicateField, ErrFieldNumZero}
	for i := range all {
		for j := i + 1; j < len(all); j++ {
			if errors.Is(all[i], all[j]) {
				t.Fatalf("sentinels %v and %v not distinct", all[i], all[j])
			}
		}
	}
	// Usable after rejection.
	good, err := Decode([]byte{0x08, 0x96, 0x01}, schema)
	if err != nil || good[1].U != 150 {
		t.Fatal("decoder unusable after rejection")
	}
}

// TestEncodeRejects covers Encode-side validation.
func TestEncodeRejects(t *testing.T) {
	cases := []struct {
		name string
		in   []Field
		want error
	}{
		{"zero", []Field{{Num: 0, Wire: WireVarint}}, ErrFieldNumZero},
		{"dup", []Field{{Num: 1, Wire: 0}, {Num: 1, Wire: 0}}, ErrDuplicateField},
		{"badwire", []Field{{Num: 1, Wire: 3}}, ErrBadWireType},
	}
	for _, c := range cases {
		if _, err := Encode(c.in); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v want %v", c.name, err, c.want)
		}
	}
}
