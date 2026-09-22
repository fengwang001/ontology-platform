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
		{Kind: Span, From: 0, To: 99},
		{Kind: Open, From: 200},
		{Kind: Suffix, N: 50},
	}
	if len(specs) != len(want) {
		t.Fatalf("got %d specs, want %d", len(specs), len(want))
	}
	for i, w := range want {
		if specs[i] != w {
			t.Errorf("spec %d = %+v, want %+v", i, specs[i], w)
		}
	}
}

func TestParseSuffixZeroIsNotSyntaxError(t *testing.T) {
	specs, err := Parse("bytes=-0")
	if err != nil {
		t.Fatalf("bytes=-0 must parse, got %v", err)
	}
	if len(specs) != 1 || specs[0].Kind != Suffix || specs[0].N != 0 {
		t.Fatalf("got %+v, want one Suffix spec with N=0", specs)
	}
}

func TestSyntaxErrorOffsets(t *testing.T) {
	cases := []struct {
		header string
		offset int
	}{
		{"items=1-2", 0},                    // wrong unit
		{"bytes=", 6},                       // empty spec
		{"bytes=1", 6},                      // missing dash
		{"bytes=a-1", 6},                    // bad start digit
		{"bytes=1-x", 8},                    // bad end digit
		{"bytes=-x", 7},                     // bad suffix digit
		{"bytes=1-2,,3-4", 10},              // empty middle spec
		{"bytes=1-2-3", 9},                  // second dash inside digits
		{"bytes=99999999999999999999-1", 6}, // overflow
	}
	for _, c := range cases {
		_, err := Parse(c.header)
		var se *SyntaxError
		if !errors.As(err, &se) {
			t.Errorf("%q: expected *SyntaxError, got %v", c.header, err)
			continue
		}
		if se.Offset != c.offset {
			t.Errorf("%q: offset = %d, want %d", c.header, se.Offset, c.offset)
		}
	}
}

func TestParseCaseInsensitiveUnit(t *testing.T) {
	if _, err := Parse("BYTES=1-2"); err != nil {
		t.Fatalf("unit should be case-insensitive: %v", err)
	}
}

func TestParseMultipleWithWhitespace(t *testing.T) {
	specs, err := Parse("bytes= 1-2 ,\t5- ")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(specs) != 2 || specs[0].From != 1 || specs[1].From != 5 {
		t.Fatalf("got %+v", specs)
	}
}
