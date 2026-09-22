package rangespec

import (
	"errors"
	"testing"
)

func TestParseThreeForms(t *testing.T) {
	specs, err := Parse("bytes=0-99, 200-, -50")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []Spec{
		{First: 0, Last: 99, Suffix: -1},
		{First: 200, Last: -1, Suffix: -1},
		{First: 0, Last: -1, Suffix: 50},
	}
	if len(specs) != len(want) {
		t.Fatalf("got %d specs, want %d", len(specs), len(want))
	}
	for i := range want {
		if specs[i] != want[i] {
			t.Errorf("spec %d = %+v, want %+v", i, specs[i], want[i])
		}
	}
}

func TestParseMinusZeroIsValidSyntax(t *testing.T) {
	// "bytes=-0" is syntactically well-formed: the "-n" form with n=0.
	// Unsatisfiability is a semantic judgment made by coalesce, not here.
	specs, err := Parse("bytes=-0")
	if err != nil {
		t.Fatalf("Parse(bytes=-0) must not be a syntax error, got %v", err)
	}
	if len(specs) != 1 || specs[0].Suffix != 0 {
		t.Fatalf("got %+v, want one suffix spec with n=0", specs)
	}
}

func TestSyntaxErrorCarriesOffset(t *testing.T) {
	cases := []struct {
		header string
		offset int
	}{
		{"items=0-1", 0},                    // wrong unit prefix
		{"bytes=", 6},                       // empty set
		{"bytes=0-1,,2-3", 10},              // empty item
		{"bytes=0-1, x-y", 11},              // non-digit start
		{"bytes=3-x", 8},                    // non-digit end
		{"bytes=-x", 7},                     // non-digit suffix
		{"bytes=5-3", 8},                    // end before start
		{"bytes=0-1, 4", 11},                // missing dash
		{"bytes=99999999999999999999-1", 6}, // overflow
	}
	for _, c := range cases {
		_, err := Parse(c.header)
		var se *SyntaxError
		if !errors.As(err, &se) {
			t.Errorf("Parse(%q): not a SyntaxError: %v", c.header, err)
			continue
		}
		if se.Offset != c.offset {
			t.Errorf("Parse(%q): offset=%d, want %d", c.header, se.Offset, c.offset)
		}
	}
}

func TestParseWhitespaceAndTabs(t *testing.T) {
	specs, err := Parse("bytes= 1-2 ,\t5- ")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(specs) != 2 || specs[0].First != 1 || specs[1].First != 5 {
		t.Fatalf("got %+v", specs)
	}
}
