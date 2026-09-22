package rng

import (
	"testing"

	"ontology/ver"
)

func p(t *testing.T, s string) Range {
	t.Helper()
	r, err := Parse(s)
	if err != nil {
		t.Fatalf("Parse(%q): %v", s, err)
	}
	return r
}

func pv(t *testing.T, s string) ver.Version {
	t.Helper()
	v, err := ver.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestContains(t *testing.T) {
	cases := []struct {
		spec string
		v    string
		want bool
	}{
		{">=1.0.0", "1.0.0", true},
		{">=1.0.0", "1.0.0-beta", false},
		{">0.9.0 <1.0.0", "1.0.0-beta", true},
		{">1.2.0 <1.2.1", "1.2.1-rc1", true},
		{">1.2.0 <1.2.1", "1.2.0", false},
		{">1.2.0 <1.2.1", "1.2.1", false},
		{"=1.2.0", "1.2.0", true},
		{"=1.2.0", "1.2.1", false},
		{"<2.0.0", "1.9.9", true},
	}
	for _, c := range cases {
		if got := p(t, c.spec).Contains(pv(t, c.v)); got != c.want {
			t.Errorf("%q.Contains(%q)=%v want %v", c.spec, c.v, got, c.want)
		}
	}
}

func TestIntersectEmpty(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{">=1.2.0", "<1.2.0", true},
		{">=1.2.0", "<=1.2.0", false},
		{">1.2.0", "<1.2.1", false},
		{">1.2.1", "<1.2.1", true},
		{">=2.0.0", "<2.0.0", true},
		{">=1.0.0 <2.0.0", ">=1.5.0 <3.0.0", false},
		{">=2.0.0", "<1.5.0", true},
	}
	for _, c := range cases {
		if got := Intersect(p(t, c.a), p(t, c.b)).IsEmpty(); got != c.want {
			t.Errorf("Intersect(%q,%q).IsEmpty()=%v want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestIntersectContains(t *testing.T) {
	r := Intersect(p(t, ">=1.0.0 <2.0.0"), p(t, ">=1.5.0"))
	if !r.Contains(pv(t, "1.9.0")) || r.Contains(pv(t, "1.4.0")) {
		t.Fatalf("intersection membership wrong: %s", r)
	}
}

func TestParseErrors(t *testing.T) {
	bad := []string{"", "1.2.0", ">>1.2.0", ">=1.x", ">= 1.2.0", "<=v1.2.0", "~1.2.0"}
	for _, s := range bad {
		if _, err := Parse(s); err != ErrSyntax {
			t.Errorf("Parse(%q) err=%v want ErrSyntax", s, err)
		}
	}
}
