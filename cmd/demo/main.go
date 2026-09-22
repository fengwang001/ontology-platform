// Command demo runs the join-order cost-model acceptance checks without
// arguments or network access, printing one OK/FAIL line per check and a
// final total.
package main

import (
	"fmt"
	"os"

	"ontology/catalog"
	"ontology/explain"
	"ontology/plan"
	"ontology/stats"
)

func main() {
	fails := 0
	check := func(desc string, ok bool) {
		status := "OK"
		if !ok {
			status = "FAIL"
			fails++
		}
		fmt.Printf("%s %s\n", status, desc)
	}

	check("tie-break picks lexicographically smaller name order", tieBreakWins())
	check("20 registration-order shuffles give identical output", shuffleStable())
	check("chain predicate graph has no cartesian product",
		!containsCartesian(chainCatalog(false)))
	check("disconnected graph has exactly one cartesian at the last step", oneRootCartesian())
	check(complexityLine(), complexityOK())
	check("missing statistics fall back and are flagged unreliable", missingOK())
	check("stale statistics are flagged and corrected to catalog rows", staleOK())
	check("corrupt statistics are rejected with ErrCorruptStats", corruptOK())
	check("n=20 exceeds the DP table cap (ErrTooManyTables)", capOK())

	if fails == 0 {
		fmt.Println("TOTAL all 9 checks passed")
	} else {
		fmt.Printf("TOTAL %d of 9 checks failed\n", fails)
	}
	os.Exit(fails)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func addTable(c *catalog.Catalog, name string, rows int64, cols map[string]float64) {
	st := &stats.Table{Name: name, Rows: rows, Columns: map[string]stats.Column{}}
	for col, ndv := range cols {
		st.Columns[col] = stats.Column{NDV: ndv}
	}
	must(c.AddTable(name, rows, st))
}

func chainCatalog(disconnected bool) *catalog.Catalog {
	c := catalog.New()
	for _, spec := range []struct {
		name string
		rows int64
	}{{"A", 1000}, {"B", 2000}, {"C", 3000}, {"D", 4000}} {
		addTable(c, spec.name, spec.rows, map[string]float64{"k": 100})
	}
	if disconnected {
		must(c.AddPredicate("A", "k", "B", "k"))
		must(c.AddPredicate("C", "k", "D", "k"))
	} else {
		must(c.AddPredicate("A", "k", "B", "k"))
		must(c.AddPredicate("B", "k", "C", "k"))
		must(c.AddPredicate("C", "k", "D", "k"))
	}
	return c
}

func containsCartesian(c *catalog.Catalog) bool {
	root, _, err := plan.Optimize(c, []string{"A", "B", "C", "D"})
	must(err)
	return contains(explain.Render(root, c), "[cartesian]")
}

func explainRender(c *catalog.Catalog, root *plan.Node) string {
	return explain.Render(root, c)
}

func render(c *catalog.Catalog) string {
	var names []string
	for _, n := range []string{"A", "B", "C", "D", "E", "F"} {
		if _, ok := c.Table(n); ok {
			names = append(names, n)
		}
	}
	root, _, err := plan.Optimize(c, names)
	must(err)
	return explain.Render(root, c)
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
