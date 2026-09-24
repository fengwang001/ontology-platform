package rangespec

import (
	"errors"
	"testing"
)

func TestParse(t *testing.T) {
	ok := map[string][]Spec{
		"bytes=0-499":        {{Kind: Closed, A: 0, B: 499}},
		"bytes=500-":         {{Kind: OpenEnd, A: 500, B: 0}},
		"bytes=-500":         {{Kind: Suffix, A: 0, B: 500}},
		"bytes=-0":           {{Kind: Suffix, A: 0, B: 0}},
		"bytes=0-499,500-999,-200,1000-": {
			{Kind: Closed, A: 0, B: 499},
			{Kind: Closed, A: 500, B: 999},
			{Kind: Suffix, A: 0, B: 200},
			{Kind: OpenEnd, A: 1000, B: 0},
		},
	}
	for in, want := range ok {
		got, err := Parse(in)
		if err != nil || len(got) != len(want) {
			t.Fatalf("Parse(%q) = %v, %v", in, got, err)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("Parse(%q)[%d] = %+v, want %+v", in, i, got[i], want[i])
			}
		}
	}

	bad := []struct {
		in   string
		want int
	}{
		{"bytes=0-499,", 12},   // empty element after comma
		{"byte=0-1", 0},       // missing prefix
		{"bytes=100", 9},      // missing '-' (index just past the token)
		{"bytes=-", 6},        // no number at all
		{"bytes=5-3", 8},      // inverted
		{"bytes=1--2", 8},     // two dashes (second '-' at offset 8)
		{"bytes=1-x", 8},      // non-digit
		{"bytes=", 6},         // empty list
		{"bytes=9223372036854775808-", 24}, // int64 overflow on last digit
	}
	for _, tc := range bad {
		_, err := Parse(tc.in)
		var se *SyntaxError
		if !errors.As(err, &se) {
			t.Fatalf("Parse(%q): want SyntaxError, got %v", tc.in, err)
		}
		if se.Offset != tc.want {
			t.Fatalf("Parse(%q): offset = %d, want %d (%s)", tc.in, se.Offset, tc.want, se)
		}
	}

	// "-0" is syntactically legal here; unsatisfiability needs a length and is
	// decided by the coalesce package.
	if specs, err := Parse("bytes=-0"); err != nil || specs[0].B != 0 || specs[0].Kind != Suffix {
		t.Fatalf("bytes=-0 must parse as a suffix spec, got %v, %v", specs, err)
	}
}
