package plan

import (
	"fmt"
	"math/rand"
	"testing"

	"ontology/catalog"
	"ontology/stats"
)

func TestCardinalitySemantics(t *testing.T) {
	cases := []struct {
		name     string
		tabs     map[string]tabSpec
		preds    [][4]string
		wantCard float64
	}{
		{"two predicates multiply selectivities",
			map[string]tabSpec{
				"A": {1000, map[string]float64{"x": 10, "y": 100}},
				"B": {500, map[string]float64{"x": 10, "y": 100}}},
			[][4]string{{"A", "x", "B", "x"}, {"A", "y", "B", "y"}},
			1000.0 * 500 * 0.1 * 0.01},
		{"all zero rows",
			map[string]tabSpec{
				"A": {0, map[string]float64{"k": 1}},
				"B": {0, map[string]float64{"k": 1}}},
			[][4]string{{"A", "k", "B", "k"}}, 0},
		{"single row table",
			map[string]tabSpec{
				"A": {1, map[string]float64{"k": 1}},
				"B": {100, map[string]float64{"k": 10}}},
			[][4]string{{"A", "k", "B", "k"}}, 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var names []string
			for n := range tc.tabs {
				names = append(names, n)
			}
			root, _, err := Optimize(mkCatalog(t, tc.tabs, tc.preds), names)
			if err != nil {
				t.Fatal(err)
			}
			if !costEqual(root.Card, tc.wantCard) {
				t.Fatalf("card=%v want %v", root.Card, tc.wantCard)
			}
		})
	}
	if got := stats.RowAccesses(); got != 0 {
		t.Fatalf("estimation touched row data %d times", got)
	}
}

func TestConcurrent(t *testing.T) {
	tabs, preds, names := chainTabs(8)
	cat := mkCatalog(t, tabs, preds)
	want, _, err := Optimize(cat, names)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan []string, 64)
	for g := 0; g < 8; g++ {
		go func() {
			for i := 0; i < 8; i++ {
				root, _, err := Optimize(cat, names)
				if err != nil {
					t.Error(err)
					return
				}
				done <- root.Key()
			}
		}()
	}
	for i := 0; i < 64; i++ {
		got := <-done
		if fmt.Sprint(got) != fmt.Sprint(want.Key()) {
			t.Fatalf("concurrent result diverged: %v vs %v", got, want.Key())
		}
	}
}

// Shuffling table registration order 20 times must give a bitwise identical
// plan key and cost (the explain test covers byte-identical rendering).
func TestShuffleDeterministic(t *testing.T) {
	tabs, preds, names := chainTabs(6)
	want, _, err := Optimize(mkCatalog(t, tabs, preds), names)
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 20; trial++ {
		shuffled := append([]string(nil), names...)
		rng.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		c := catalog.New()
		for _, n := range shuffled {
			ts := tabs[n]
			st := &stats.Table{Name: n, Rows: ts.rows,
				Columns: map[string]stats.Column{"k": {NDV: 50}}}
			if err := c.AddTable(n, ts.rows, st); err != nil {
				t.Fatal(err)
			}
		}
		for _, p := range preds {
			if err := c.AddPredicate(p[0], p[1], p[2], p[3]); err != nil {
				t.Fatal(err)
			}
		}
		got, _, err := Optimize(c, names)
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(got.Key()) != fmt.Sprint(want.Key()) || got.Cost != want.Cost {
			t.Fatalf("trial %d diverged: key=%v cost=%v", trial, got.Key(), got.Cost)
		}
	}
}
