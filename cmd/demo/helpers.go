package main

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"

	"ontology/catalog"
	"ontology/plan"
	"ontology/stats"
)

func tieBreakWins() bool {
	c := catalog.New()
	addTable(c, "A", 1000000, map[string]float64{"ab": 3, "ac": 12})
	addTable(c, "B", 2, map[string]float64{"b": 3})
	addTable(c, "C", 4, map[string]float64{"c": 12})
	must(c.AddPredicate("A", "ab", "B", "b"))
	must(c.AddPredicate("A", "ac", "C", "c"))
	root, _, err := plan.Optimize(c, []string{"A", "B", "C"})
	return err == nil && fmt.Sprint(root.Key()) == "[A B C]"
}

func shuffleStable() bool {
	tie := func(order []string) *catalog.Catalog {
		c := catalog.New()
		cols := map[string]map[string]float64{
			"A": {"ab": 3, "ac": 12},
			"B": {"b": 3},
			"C": {"c": 12},
		}
		rows := map[string]int64{"A": 1000000, "B": 2, "C": 4}
		for _, n := range order {
			addTable(c, n, rows[n], cols[n])
		}
		must(c.AddPredicate("A", "ab", "B", "b"))
		must(c.AddPredicate("A", "ac", "C", "c"))
		return c
	}
	names := []string{"A", "B", "C"}
	want := render(tie(names))
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 20; trial++ {
		order := append([]string(nil), names...)
		rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
		if render(tie(order)) != want {
			return false
		}
	}
	return true
}

func oneRootCartesian() bool {
	c := chainCatalog(true)
	root, _, err := plan.Optimize(c, []string{"A", "B", "C", "D"})
	return err == nil && root.Cartesian && countCartesian(root) == 1
}

func countCartesian(n *plan.Node) int {
	if n.Leaf() {
		return 0
	}
	k := countCartesian(n.Left) + countCartesian(n.Right)
	if n.Cartesian {
		k++
	}
	return k
}

func complexityLine() string {
	_, m, _ := plan.Optimize(nChainCatalog(12), nChainNames(12))
	return fmt.Sprintf("n=12: subsets=%d<=4096 splits=%d<=531441 both << 12!=479001600",
		m.Subsets(), m.Splits())
}

func complexityOK() bool {
	_, m, err := plan.Optimize(nChainCatalog(12), nChainNames(12))
	return err == nil && m.Subsets() <= 1<<12 && m.Splits() < 531441 &&
		m.Splits()*100 < 479001600
}

func nChainNames(n int) []string {
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("T%02d", i)
	}
	return names
}

func nChainCatalog(n int) *catalog.Catalog {
	c := catalog.New()
	names := nChainNames(n)
	for _, name := range names {
		addTable(c, name, 1000, map[string]float64{"k": 50})
	}
	for i := 1; i < n; i++ {
		must(c.AddPredicate(names[i-1], "k", names[i], "k"))
	}
	return c
}

func missingOK() bool {
	c := catalog.New()
	must(c.AddTable("A", 100, nil))
	addTable(c, "B", 100, map[string]float64{"k": 10})
	must(c.AddPredicate("A", "k", "B", "k"))
	root, _, err := plan.Optimize(c, []string{"A", "B"})
	if err != nil || root == nil {
		return false
	}
	out := explainRender(c, root)
	return findErr(c, catalog.ErrMissingStats) != nil &&
		strings.Contains(out, "[unreliable estimate]")
}

func staleOK() bool {
	c := catalog.New()
	stA := &stats.Table{Name: "A", Rows: 1000, Columns: map[string]stats.Column{"k": {NDV: 10}}}
	must(c.AddTable("A", 100, stA))
	addTable(c, "B", 100, map[string]float64{"k": 10})
	must(c.AddPredicate("A", "k", "B", "k"))
	root, _, err := plan.Optimize(c, []string{"A", "B"})
	if err != nil || root == nil {
		return false
	}
	info, _ := c.Table("A")
	return info.Stale && findErr(c, catalog.ErrStaleStats) != nil &&
		strings.Contains(explainRender(c, root), "[stale stats: catalog rows 100 used]")
}

func corruptOK() bool {
	c := catalog.New()
	st := &stats.Table{Name: "A", Rows: 2, Columns: map[string]stats.Column{
		"k": {Histogram: &stats.Histogram{Bounds: []float64{0, 0, 1}, Buckets: []uint64{1, 1}}}}}
	return errors.Is(c.AddTable("A", 2, st), catalog.ErrCorruptStats)
}

func capOK() bool {
	_, _, err := plan.Optimize(nChainCatalog(20), nChainNames(20))
	return errors.Is(err, plan.ErrTooManyTables)
}

func findErr(c *catalog.Catalog, target error) error {
	for _, e := range c.Check() {
		if errors.Is(e, target) {
			return e
		}
	}
	return nil
}
