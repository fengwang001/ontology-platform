package semver

import (
	"errors"
	"testing"
)

func parseRange(t *testing.T, s string) Range {
	t.Helper()
	r, err := ParseRange(s)
	if err != nil {
		t.Fatalf("ParseRange(%q): %v", s, err)
	}
	return r
}

func TestParseRangeErrors(t *testing.T) {
	for _, s := range []string{"", "   ", "1.2.3", ">>1.2.3", ">=nope", "^01.0.0"} {
		if _, err := ParseRange(s); err == nil {
			t.Errorf("ParseRange(%q) expected error", s)
		} else if s == "" || s == "   " {
			if !errors.Is(err, ErrEmptyRange) {
				t.Errorf("ParseRange(%q) err = %v, want ErrEmptyRange", s, err)
			}
		} else if !errors.Is(err, ErrInvalidRange) {
			t.Errorf("ParseRange(%q) err = %v, want ErrInvalidRange", s, err)
		}
	}
}

func TestComparatorMatch(t *testing.T) {
	cases := []struct {
		expr string
		v    string
		want bool
	}{
		{">=1.2.3", "1.2.3", true},
		{">=1.2.3", "1.3.0", true},
		{">=1.2.3", "1.2.2", false},
		{">1.2.3", "1.2.3", false},
		{">1.2.3", "1.2.4", true},
		{"<=1.2.3", "1.2.3", true},
		{"<1.2.3", "1.2.3", false},
		{"<1.2.3", "1.2.2", true},
		{"=1.2.3", "1.2.3+build", true},
		{"=1.2.3", "1.2.4", false},
	}
	for _, c := range cases {
		r := parseRange(t, c.expr)
		if got := r.Match(mustParse(t, c.v)); got != c.want {
			t.Errorf("%q.Match(%q) = %v, want %v", c.expr, c.v, got, c.want)
		}
	}
}

func TestCaretExpansion(t *testing.T) {
	cases := []struct {
		expr string
		in   string
		want bool
	}{
		{"^1.2.3", "1.2.3", true},
		{"^1.2.3", "1.9.9", true},
		{"^1.2.3", "2.0.0", false},
		{"^1.2.3", "1.2.2", false},
		{"^0.2.3", "0.2.3", true},
		{"^0.2.3", "0.2.9", true},
		{"^0.2.3", "0.3.0", false},
		{"^0.0.3", "0.0.3", true},
		{"^0.0.3", "0.0.4", false},
		{"^0.0.3", "0.0.2", false},
	}
	for _, c := range cases {
		r := parseRange(t, c.expr)
		if got := r.Match(mustParse(t, c.in)); got != c.want {
			t.Errorf("%q.Match(%q) = %v, want %v", c.expr, c.in, got, c.want)
		}
	}
}

func TestTildeExpansion(t *testing.T) {
	r := parseRange(t, "~1.2.3")
	for _, c := range []struct {
		v    string
		want bool
	}{
		{"1.2.3", true},
		{"1.2.9", true},
		{"1.3.0", false},
		{"1.2.2", false},
	} {
		if got := r.Match(mustParse(t, c.v)); got != c.want {
			t.Errorf("~1.2.3.Match(%q) = %v, want %v", c.v, got, c.want)
		}
	}
}

func TestAND(t *testing.T) {
	r := parseRange(t, ">=1.2.0 <1.3.0")
	if !r.Match(mustParse(t, "1.2.5")) {
		t.Error("1.2.5 should match")
	}
	if r.Match(mustParse(t, "1.3.0")) {
		t.Error("1.3.0 should not match")
	}
	if r.Match(mustParse(t, "1.1.9")) {
		t.Error("1.1.9 should not match")
	}
}
