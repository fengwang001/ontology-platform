package rangespec

import (
	"errors"
	"testing"
)

func TestParseThreeForms(t *testing.T) {
	cases := []struct {
		header string
		want   []Interval
	}{
		{"bytes=10-20", []Interval{{Start: 10, End: 20}}},
		{"bytes=10-", []Interval{{Start: 10, End: -1}}},
		{"bytes=-10", []Interval{{Start: -1, End: 10}}},
		{"BYTES= 1-2 , 3- , -4 ", []Interval{{1, 2}, {3, -1}, {-1, 4}}},
	}

	for _, tc := range cases {
		got, err := Parse(tc.header)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", tc.header, err)
		}
		if len(got) != len(tc.want) {
			t.Fatalf("Parse(%q) intervals = %v, want %v", tc.header, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("Parse(%q)[%d] = %+v, want %+v", tc.header, i, got[i], tc.want[i])
			}
		}
	}
}

func TestParsePreservesSuffixZero(t *testing.T) {
	got, err := Parse("bytes=-0")
	if err != nil {
		t.Fatalf("Parse returned syntax error: %v", err)
	}
	if len(got) != 1 || got[0] != (Interval{Start: -1, End: 0}) {
		t.Fatalf("suffix zero = %v, want {-1 0}", got)
	}
}

func TestParseSyntaxErrorsHaveOffsets(t *testing.T) {
	cases := []struct {
		header string
		offset int
	}{
		{"items=1-2", 0},
		{"bytes1-2", 5},
		{"bytes=", 6},
		{"bytes=1", 7},
		{"bytes=10-x", 9},
		{"bytes=10-5", 6},
		{"bytes=1-2,,3-4", 10},
		{"bytes=1-2;", 9},
		{"bytes=999999999999999999999999", 6},
	}

	for _, tc := range cases {
		_, err := Parse(tc.header)
		var syntaxErr SyntaxError
		if !errors.As(err, &syntaxErr) {
			t.Fatalf("Parse(%q) error %v, want SyntaxError", tc.header, err)
		}
		if syntaxErr.Offset != tc.offset {
			t.Fatalf("Parse(%q) offset = %d, want %d", tc.header, syntaxErr.Offset, tc.offset)
		}
	}
}

func TestIsSyntaxError(t *testing.T) {
	_, err := Parse("bad")
	if !IsSyntaxError(err) {
		t.Fatalf("IsSyntaxError(Parse error) = false")
	}
	if IsSyntaxError(nil) {
		t.Fatalf("IsSyntaxError(nil) = true")
	}
}
