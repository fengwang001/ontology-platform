package rangespec

import "testing"

func TestParseThreeForms(t *testing.T) {
	specs, err := Parse("bytes=10-20, 30-, -5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(specs) != 3 {
		t.Fatalf("got %d specs, want 3", len(specs))
	}
	want := []Spec{
		{Kind: Closed, First: 10, Last: 20},
		{Kind: From, First: 30},
		{Kind: Suffix, First: 5},
	}
	for i := range want {
		if specs[i] != want[i] {
			t.Fatalf("spec %d = %+v, want %+v", i, specs[i], want[i])
		}
	}
}

func TestParseSuffixZeroIsSyntacticallyValid(t *testing.T) {
	specs, err := Parse("bytes=-0")
	if err != nil {
		t.Fatalf("bytes=-0 must parse; unsatisfiability is resolved later: %v", err)
	}
	if len(specs) != 1 || specs[0] != (Spec{Kind: Suffix, First: 0}) {
		t.Fatalf("unexpected specs: %+v", specs)
	}
}

func TestParseSyntaxErrorOffsets(t *testing.T) {
	cases := map[string]int{
		"bytes=10-x":      9,
		"bytes=10-20 30":  11,
		"items=0-10":      0,
		"bytes=10_20":     11,
		"bytes=20-10":     9,
		"bytes=-":         7,
		"bytes=,":         6,
		"bytes":           0,
		"bytes=":          6,
	}
	for in, wantOff := range cases {
		_, err := Parse(in)
		se, ok := AsSyntaxError(err)
		if !ok {
			t.Errorf("%q: expected *SyntaxError, got %v", in, err)
			continue
		}
		if se.Offset != wantOff {
			t.Errorf("%q: offset = %d, want %d (%s)", in, se.Offset, wantOff, se.Msg)
		}
	}
}

func TestParseLargeValues(t *testing.T) {
	specs, err := Parse("bytes=0-9223372036854775807")
	if err != nil {
		t.Fatalf("max int64 should parse: %v", err)
	}
	if specs[0].Last != 1<<63-1 {
		t.Fatalf("got last=%d", specs[0].Last)
	}
	if _, err := Parse("bytes=0-9223372036854775808"); err == nil {
		t.Fatal("overflow must be a syntax error")
	}
}
