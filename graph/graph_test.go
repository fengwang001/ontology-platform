package graph

import (
	"errors"
	"testing"

	"ontology/rng"
	"ontology/ver"
)

func TestAddAndQuery(t *testing.T) {
	g := New()
	if err := g.AddVersion("a", "2.0.0", []Dep{{Target: "b", Constraint: ">=1.0.0"}}...); err != nil {
		t.Fatal(err)
	}
	if err := g.AddVersion("a", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	vs, ok := g.Versions("a")
	if !ok || len(vs) != 2 || vs[0].Compare(ver.MustParse("1.0.0")) != 0 {
		t.Fatalf("versions not sorted: %v", vs)
	}
	if g.Packages()[0] != "a" {
		t.Fatal("packages order")
	}
	d := g.Deps(Ref{"a", ver.MustParse("2.0.0")})
	if len(d) != 1 || d[0].Origin.Target != "b" || !d[0].Range.Contains(ver.MustParse("1.5.0")) {
		t.Fatalf("bad deps: %v", d)
	}
	if _, ok := g.Versions("ghost"); ok {
		t.Fatal("unknown package should fail lookup")
	}
}

func TestAddErrors(t *testing.T) {
	cases := []struct {
		name string
		fn   func(g *Graph) error
		want error
	}{
		{"bad-version", func(g *Graph) error { return g.AddVersion("a", "v1") }, ver.ErrInvalidVersion},
		{"bad-constraint", func(g *Graph) error {
			return g.AddVersion("a", "1.0.0", []Dep{{Target: "b", Constraint: "~1"}}...)
		}, rng.ErrInvalidConstraint},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.fn(New()); !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want %v", err, tc.want)
			}
		})
	}
}

func TestDuplicateDoesNotMutate(t *testing.T) {
	g := New()
	if err := g.AddVersion("a", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	err := g.AddVersion("a", "1.0.0", []Dep{{Target: "b", Constraint: ">=1.0.0"}}...)
	if !errors.Is(err, ErrDuplicateVersion) {
		t.Fatalf("err=%v", err)
	}
	if len(g.Deps(Ref{"a", ver.MustParse("1.0.0")})) != 0 {
		t.Fatal("duplicate registration must not mutate existing entry")
	}
	vs, _ := g.Versions("a")
	if len(vs) != 1 || g.HasPackage("b") {
		t.Fatal("duplicate registration must not add packages or versions")
	}
}

func TestDepOrderIndependent(t *testing.T) {
	deps := []Dep{{"z", ">=1.0.0"}, {"a", "<2.0.0"}, {"m", "*"}}
	g := New()
	if err := g.AddVersion("x", "1.0.0", deps...); err != nil {
		t.Fatal(err)
	}
	got := g.Deps(Ref{"x", ver.MustParse("1.0.0")})
	if got[0].Origin.Target != "a" || got[1].Origin.Target != "m" || got[2].Origin.Target != "z" {
		t.Fatalf("deps not sorted: %v", got)
	}
}
