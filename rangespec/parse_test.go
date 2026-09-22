package rangespec

import (
	"errors"
	"testing"
)

func TestParseForms(t *testing.T) {
	cases := []struct {
		header string
		want   []RangeSpec
	}{
		{"bytes=0-99", []RangeSpec{{Kind: KindClosed, Start: 0, End: 99}}},
		{"bytes=100-", []RangeSpec{{Kind: KindOpenEnd, Start: 100}}},
		{"bytes=-500", []RangeSpec{{Kind: KindSuffix, Suffix: 500}}},
		{"bytes= 0-1 , 2- , -3", []RangeSpec{
			{Kind: KindClosed, Start: 0, End: 1},
			{Kind: KindOpenEnd, Start: 2},
			{Kind: KindSuffix, Suffix: 3},
		}},
		{"BYTES=0-0", []RangeSpec{{Kind: KindClosed, Start: 0, End: 0}}},
	}
	for _, c := range cases {
		got, err := Parse(c.header)
		if err != nil {
			t.Fatalf("%q: unexpected error %v", c.header, err)
		}
		if len(got) != len(c.want) {
			t.Fatalf("%q: got %d specs, want %d", c.header, len(got), len(c.want))
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%q[%d] = %+v, want %+v", c.header, i, got[i], c.want[i])
			}
		}
	}
}

func TestSyntaxErrorsCarryOffset(t *testing.T) {
	cases := map[string]int{
		"items=0-1":                       0,
		"bytes":                           5,
		"bytes =":                         7,
		"bytes=":                          6,
		"bytes=0":                         7,
		"bytes=-":                         7,
		"bytes=0-1,":                      9,
		"bytes=0-1 x":                     10,
		"bytes=1--2":                      8,
		"bytes=999999999999999999999999-": 6,
	}
	for header, wantOffset := range cases {
		_, err := Parse(header)
		var se *SyntaxError
		if !errors.As(err, &se) {
			t.Fatalf("%q: want *SyntaxError, got %v", header, err)
		}
		if se.Offset != wantOffset {
			t.Errorf("%q: offset = %d, want %d (%s)", header, se.Offset, wantOffset, se.Reason)
		}
	}
}

func TestMinusZeroIsSyntacticallyValid(t *testing.T) {
	specs, err := Parse("bytes=-0")
	if err != nil {
		t.Fatalf("bytes=-0 must parse (unsatisfiability is semantic): %v", err)
	}
	if len(specs) != 1 || specs[0].Kind != KindSuffix || specs[0].Suffix != 0 {
		t.Fatalf("got %+v", specs)
	}
}
