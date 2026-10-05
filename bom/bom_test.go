package bom_test

import (
	"errors"
	"testing"

	"ontology/bom"
)

func TestAddComponentValidation(t *testing.T) {
	cases := []struct {
		name          string
		parent, child string
		per, scrap    int64
		wantErr       error
	}{
		{"ok", "A", "B", 1, 0, nil},
		{"ok-max", "A", "C", 10_000, 999, nil},
		{"per-zero", "A", "D", 0, 0, bom.ErrInvalid},
		{"per-too-big", "A", "D", 10_001, 0, bom.ErrInvalid},
		{"scrap-negative", "A", "D", 1, -1, bom.ErrInvalid},
		{"scrap-too-big", "A", "D", 1, 1000, bom.ErrInvalid},
		{"empty-parent", "", "D", 1, 0, bom.ErrInvalid},
		{"empty-child", "A", "", 1, 0, bom.ErrInvalid},
	}
	for _, c := range cases {
		g := bom.New()
		err := g.AddComponent(c.parent, c.child, c.per, c.scrap)
		if !errors.Is(err, c.wantErr) {
			t.Errorf("%s: got %v want %v", c.name, err, c.wantErr)
		}
		if c.wantErr != nil && g.Version() != 0 {
			t.Errorf("%s: rejected AddComponent bumped version", c.name)
		}
	}
}

func TestConflictAndCycle(t *testing.T) {
	g := bom.New()
	add := func(p, c string) error { return g.AddComponent(p, c, 1, 0) }
	if err := add("A", "B"); err != nil {
		t.Fatal(err)
	}
	if err := add("B", "C"); err != nil {
		t.Fatal(err)
	}
	if err := add("A", "B"); !errors.Is(err, bom.ErrConflict) {
		t.Fatalf("duplicate: got %v", err)
	}
	if err := add("C", "A"); !errors.Is(err, bom.ErrCycle) {
		t.Fatalf("3-cycle: got %v", err)
	}
	if err := add("C", "B"); !errors.Is(err, bom.ErrCycle) {
		t.Fatalf("2-cycle via path: got %v", err)
	}
	if err := add("A", "A"); !errors.Is(err, bom.ErrCycle) {
		t.Fatalf("self-loop: got %v", err)
	}
	if g.EdgeCount() != 2 || g.Version() != 2 {
		t.Fatalf("rejected ops changed state: edges=%d version=%d", g.EdgeCount(), g.Version())
	}
}

func TestLowCodes(t *testing.T) {
	g := bom.New()
	edges := [][2]string{
		{"A", "B"}, {"A", "C"}, {"B", "D"}, {"C", "D"},
		{"A", "E"}, {"E", "F"}, {"F", "D"},
	}
	for _, e := range edges {
		if err := g.AddComponent(e[0], e[1], 1, 0); err != nil {
			t.Fatal(err)
		}
	}
	codes := g.LowCodes()
	want := map[string]int{"A": 0, "B": 1, "C": 1, "E": 1, "F": 2, "D": 3}
	for n, w := range want {
		if codes[n] != w {
			t.Errorf("lowCode(%s)=%d want %d", n, codes[n], w)
		}
	}
}

func TestParentsSorted(t *testing.T) {
	g := bom.New()
	for _, p := range []string{"z", "M", "aa", "AB"} {
		if err := g.AddComponent(p, "child", 2, 100); err != nil {
			t.Fatal(err)
		}
	}
	ps := g.Parents("child")
	want := []string{"AB", "M", "aa", "z"}
	if len(ps) != len(want) {
		t.Fatalf("got %d parents", len(ps))
	}
	for i, p := range ps {
		if p.Parent != want[i] || p.Per != 2 || p.Scrap != 100 {
			t.Errorf("Parents()[%d]=%+v want parent %q", i, p, want[i])
		}
	}
	if got := g.Parents("none"); len(got) != 0 {
		t.Errorf("unknown child: got %v", got)
	}
}
