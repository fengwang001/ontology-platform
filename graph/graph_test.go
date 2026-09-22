package graph

import (
	"errors"
	"testing"

	"ontology/rng"
)

func TestAddAndOrder(t *testing.T) {
	g := New()
	// Register versions out of order; storage must canonicalize to ascending.
	if err := g.Add("a", "1.0.0", nil); err != nil {
		t.Fatal(err)
	}
	if err := g.Add("a", "1.10.0", nil); err != nil {
		t.Fatal(err)
	}
	if err := g.Add("a", "1.2.0", nil); err != nil {
		t.Fatal(err)
	}
	got := g.Versions("a")
	want := []string{"1.0.0", "1.2.0", "1.10.0"}
	if len(got) != len(want) {
		t.Fatalf("versions=%v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("versions=%v want %v", got, want)
		}
	}
}

func TestDuplicateDoesNotMutate(t *testing.T) {
	g := New()
	if err := g.Add("a", "1.0.0", map[string]string{"b": ">=1.0.0"}); err != nil {
		t.Fatal(err)
	}
	err := g.Add("a", "1.0.0", map[string]string{"b": ">=2.0.0"})
	if !errors.Is(err, ErrDuplicateVersion) {
		t.Fatalf("err=%v want ErrDuplicateVersion", err)
	}
	r, ok := g.Constraint("a", "1.0.0", "b")
	if !ok || r.String() != ">=1.0.0" {
		t.Fatalf("existing registration mutated: %v %q", ok, r)
	}
}

func TestAddValidation(t *testing.T) {
	cases := []struct {
		name    string
		pkg     string
		version string
		deps    map[string]string
		want    error
	}{
		{"bad version", "a", "1.0", nil, nil},
		{"bad spec", "a", "1.0.0", map[string]string{"b": "~1.0"}, rng.ErrSyntax},
	}
	for _, c := range cases {
		g := New()
		err := g.Add(c.pkg, c.version, c.deps)
		if err == nil {
			t.Errorf("%s: want error", c.name)
		}
		if c.want != nil && !errors.Is(err, c.want) {
			t.Errorf("%s: err=%v want %v", c.name, err, c.want)
		}
	}
}

func TestDepsSorted(t *testing.T) {
	g := New()
	err := g.Add("a", "1.0.0", map[string]string{
		"c": ">=1.0.0",
		"b": ">=2.0.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	deps := g.Deps("a", "1.0.0")
	if len(deps) != 2 || deps[0].Pkg != "b" || deps[1].Pkg != "c" {
		t.Fatalf("deps not sorted: %+v", deps)
	}
	if !g.Has("a") || g.Has("zzz") {
		t.Fatal("Has wrong")
	}
}
