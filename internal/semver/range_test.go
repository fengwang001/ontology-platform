package semver

import (
	"errors"
	"testing"
)

func TestParseRangeInvalid(t *testing.T) {
	bad := []string{
		"", "   ", ">=1.2", ">>1.2.3", "^01.0.0", "~1.2.3-01",
		">=1.2.3 <", "=1.2.3.4",
	}
	for _, in := range bad {
		if _, err := ParseRange(in); err == nil {
			t.Errorf("ParseRange(%q) = nil error", in)
		} else if !errors.Is(err, ErrInvalidRange) {
			t.Errorf("ParseRange(%q) error %v does not wrap ErrInvalidRange", in, err)
		}
	}
}

func TestRangeComparators(t *testing.T) {
	cases := []struct {
		rng, ver string
		want     bool
	}{
		{">=1.2.3", "1.2.3", true},
		{">=1.2.3", "1.2.4", true},
		{">=1.2.3", "1.2.2", false},
		{">1.2.3", "1.2.3", false},
		{"<=1.2.3", "1.2.3", true},
		{"<1.2.3", "1.2.3", false},
		{"=1.2.3", "1.2.3", true},
		{"=1.2.3", "1.2.4", false},
		{">=1.0.0 <2.0.0", "1.9.9", true},
		{">=1.0.0 <2.0.0", "2.0.0", false},
	}
	for _, c := range cases {
		r, err := ParseRange(c.rng)
		if err != nil {
			t.Fatalf("ParseRange(%q): %v", c.rng, err)
		}
		v := mustParse(t, c.ver)
		if got := r.Match(v); got != c.want {
			t.Errorf("%q.Match(%q) = %v, want %v", c.rng, c.ver, got, c.want)
		}
	}
}

func TestCaretExpansion(t *testing.T) {
	cases := []struct {
		rng, ver string
		want     bool
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
	}
	for _, c := range cases {
		r := mustRange(t, c.rng)
		if got := r.Match(mustParse(t, c.ver)); got != c.want {
			t.Errorf("%q.Match(%q) = %v, want %v", c.rng, c.ver, got, c.want)
		}
	}
}

func TestTildeExpansion(t *testing.T) {
	r := mustRange(t, "~1.2.3")
	for _, c := range []struct {
		ver  string
		want bool
	}{
		{"1.2.3", true}, {"1.2.9", true}, {"1.3.0", false}, {"1.2.2", false},
	} {
		if got := r.Match(mustParse(t, c.ver)); got != c.want {
			t.Errorf("~1.2.3.Match(%q) = %v, want %v", c.ver, got, c.want)
		}
	}
}

func TestPrereleaseGate(t *testing.T) {
	cases := []struct {
		rng, ver string
		want     bool
	}{
		{">=1.0.0", "1.1.0-alpha", false},
		{">=1.1.0-0", "1.1.0-alpha", true},
		{">=1.1.0-0", "1.1.0", true},
		{">=1.0.0", "1.0.0-alpha", false},
		{">=1.0.0-alpha", "1.0.0-alpha", true},
		{">=1.0.0-alpha", "1.0.0-beta", true},
		{">=1.0.0-alpha", "1.1.0-alpha", false},
		{"^1.2.3-alpha", "1.2.3-beta", true},
		{"^1.2.3-alpha", "1.2.4-alpha", false},
		{"^1.2.3-alpha", "1.2.3", true},
		{"~1.2.3-alpha", "1.2.3-beta", true},
		{"=1.2.3-alpha", "1.2.3-alpha", true},
		{"=1.2.3-alpha", "1.2.3-beta", false},
		{">=1.2.3 <2.0.0", "1.5.0-rc.1", false},
		{">=1.2.3 <2.0.0-0", "1.9.9-x", false},
	}
	for _, c := range cases {
		r := mustRange(t, c.rng)
		if got := r.Match(mustParse(t, c.ver)); got != c.want {
			t.Errorf("%q.Match(%q) = %v, want %v", c.rng, c.ver, got, c.want)
		}
	}
}

func mustRange(t *testing.T, s string) Range {
	t.Helper()
	r, err := ParseRange(s)
	if err != nil {
		t.Fatalf("ParseRange(%q): %v", s, err)
	}
	return r
}
