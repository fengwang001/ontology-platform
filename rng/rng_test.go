package rng

import (
	"errors"
	"testing"

	"ontology/ver"
)

func mustParse(t *testing.T, s string) *Constraint {
	t.Helper()
	c, err := Parse(s)
	if err != nil {
		t.Fatalf("Parse(%q): %v", s, err)
	}
	return c
}

func TestParse(t *testing.T) {
	cases := []struct {
		in      string
		wantErr bool
	}{
		{">=1.2.0", false},
		{">=1.2.0 <2.0.0", false},
		{"1.2.3", false},
		{"=1.2.3", false},
		{">0.0.0", false},
		{"", true},
		{"  ", true},
		{">=", true},
		{">=x.y.z", true},
		{"~1.2.0", true},
		{">=1.2.0 and <2.0.0", true},
		{"!=1.0.0", true},
	}
	for _, c := range cases {
		_, err := Parse(c.in)
		if gotErr := err != nil; gotErr != c.wantErr {
			t.Errorf("Parse(%q) err=%v, wantErr=%v", c.in, err, c.wantErr)
		}
		if err != nil && !errors.Is(err, ErrInvalidConstraint) {
			t.Errorf("Parse(%q) error %v does not unwrap ErrInvalidConstraint", c.in, err)
		}
	}
}

func TestContains(t *testing.T) {
	cases := []struct {
		con, version string
		want         bool
	}{
		{">=1.2.0 <2.0.0", "1.2.0", true},
		{">=1.2.0 <2.0.0", "1.9.9", true},
		{">=1.2.0 <2.0.0", "2.0.0", false},
		{">=1.2.0 <2.0.0", "1.1.9", false},
		{">=1.0.0", "1.0.0-beta", false},
		{">=1.0.0-alpha", "1.0.0-beta", true},
		{">=1.0.0-alpha", "1.0.0", true},
		{"<1.0.0", "1.0.0-beta", true},
		{"1.2.3", "1.2.3", true},
		{"1.2.3", "1.2.4", false},
		{">1.2.0", "1.2.0", false},
		{"<=1.2.0", "1.2.0", true},
	}
	for _, c := range cases {
		v, err := ver.Parse(c.version)
		if err != nil {
			t.Fatalf("ver.Parse(%q): %v", c.version, err)
		}
		if got := mustParse(t, c.con).Contains(v); got != c.want {
			t.Errorf("%q.Contains(%q)=%v, want %v", c.con, c.version, got, c.want)
		}
	}
}

func TestIntersectEmptiness(t *testing.T) {
	cases := []struct {
		a, b      string
		wantEmpty bool
	}{
		{">=1.2.0", "<1.2.0", true},
		{">=1.2.0", "<=1.2.0", false},
		{">1.2.0", "<1.2.1", false},
		{">1.2.0", "<1.2.0", true},
		{">=1.2.0 <2.0.0", ">=1.5.0 <1.6.0", false},
		{">=1.2.0 <2.0.0", ">=2.0.0", true},
		{"1.2.3", "1.2.3", false},
		{"1.2.3", "1.2.4", true},
		{">=1.0.0", "<2.0.0", false},
		{"<=1.0.0", ">=1.0.0", false},
	}
	for _, c := range cases {
		inter := mustParse(t, c.a).Intersect(mustParse(t, c.b))
		if got := inter.IsEmpty(); got != c.wantEmpty {
			t.Errorf("(%q ∩ %q).IsEmpty()=%v, want %v", c.a, c.b, got, c.wantEmpty)
		}
		inter2 := mustParse(t, c.b).Intersect(mustParse(t, c.a))
		if got := inter2.IsEmpty(); got != c.wantEmpty {
			t.Errorf("(%q ∩ %q).IsEmpty()=%v, want %v", c.b, c.a, got, c.wantEmpty)
		}
	}
}
