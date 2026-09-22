package rng

import (
	"errors"
	"testing"

	"ontology/ver"
)

func mustRange(t *testing.T, s string) Range {
	t.Helper()
	r, err := Parse(s)
	if err != nil {
		t.Fatalf("Parse(%q): %v", s, err)
	}
	return r
}

func TestParseInvalid(t *testing.T) {
	cases := []string{"?", "~1.0.0", ">=", ">=1.2", "1.0.0", ">=v1.0.0", "==1.0.0"}
	for _, s := range cases {
		t.Run(s, func(t *testing.T) {
			if _, err := Parse(s); !errors.Is(err, ErrInvalidConstraint) {
				t.Fatalf("Parse(%q) err=%v", s, err)
			}
		})
	}
}

func TestContains(t *testing.T) {
	cases := []struct {
		constraint string
		version    string
		want       bool
	}{
		{"*", "9.9.9", true},
		{">=1.2.0", "1.2.0", true},
		{">1.2.0", "1.2.0", false},
		{"<2.0.0", "2.0.0", false},
		{"<=2.0.0", "2.0.0", true},
		{">=1.0.0", "1.0.0-beta", false},
		{">=1.0.0-beta", "1.0.0-beta", true},
		{">1.2.0 <1.2.1", "1.2.1-rc1", true},
		{">1.2.0 <1.2.1", "1.2.0", false},
		{"=1.2.0", "1.2.0", true},
		{"=1.2.0", "1.2.1", false},
	}
	for _, tc := range cases {
		t.Run(tc.constraint+"|"+tc.version, func(t *testing.T) {
			if got := mustRange(t, tc.constraint).Contains(ver.MustParse(tc.version)); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestIntersectEmpty(t *testing.T) {
	cases := []struct {
		name      string
		a, b      string
		wantEmpty bool
	}{
		{"closed-open-point", ">=1.2.0", "<1.2.0", true},
		{"closed-closed-point", ">=1.2.0", "<=1.2.0", false},
		{"open-closed-point", ">1.2.0", "<=1.2.0", true},
		{"pre-gap", ">1.2.0", "<1.2.1", false},
		{"wide", ">=1.0.0 <2.0.0", ">=1.5.0 <3.0.0", false},
		{"disjoint", "<1.0.0", ">=2.0.0", true},
		{"self-contradiction", ">=2.0.0 <1.0.0", "*", true},
		{"exact-in-gap", "=1.2.0", ">1.2.0 <1.2.1", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mustRange(t, tc.a).Intersect(mustRange(t, tc.b)).Empty()
			if got != tc.wantEmpty {
				t.Fatalf("Empty()=%v want %v", got, tc.wantEmpty)
			}
		})
	}
}

func TestIntersectTightens(t *testing.T) {
	r := mustRange(t, ">=1.0.0 <3.0.0").Intersect(mustRange(t, ">=2.0.0 <=2.5.0"))
	if r.Empty() || !r.Contains(ver.MustParse("2.1.0")) || r.Contains(ver.MustParse("1.5.0")) {
		t.Fatalf("bad intersection: %s", r)
	}
	if r.String() != ">=2.0.0 <=2.5.0" {
		t.Fatalf("String=%q", r.String())
	}
}
