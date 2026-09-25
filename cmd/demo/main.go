package main

import (
	"fmt"
	"os"

	"ontology/agg"
	"ontology/change"
)

var fail int

func main() {
	g := "grp"
	c := change.Change{Ver: 1, Op: change.Insert, ID: 1, Group: &g, Val: 2.5}
	got, err := change.Decode(c.Encode())
	check("change encode/decode roundtrip", err == nil && got.Val == 2.5 && got.Ver == 1)

	missing := change.Change{Ver: 2, Op: change.Insert, ID: 2, Val: 1}
	check("change rejects missing group", missing.Validate() == change.ErrNoGroup)

	mn := agg.New(agg.Min)
	mn.Add(1)
	mn.Add(2)
	check("agg Min flags recompute only when extremum removed", mn.Remove(1) && !mn.Remove(2))
	check("agg Sum never flags recompute", func() bool {
		s := agg.New(agg.Sum)
		s.Add(4)
		return !s.Remove(4) && s.Value() == 0
	}())
	dc := agg.New(agg.DistinctCount)
	dc.Add(5)
	dc.Add(5)
	dc.Rebuild([]float64{5})
	check("agg DistinctCount rebuild collapses duplicates", dc.Value() == 1)

	fmt.Printf("TOTAL: %d failed\n", fail)
	if fail != 0 {
		os.Exit(1)
	}
}

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK " + name)
		return
	}
	fail++
	fmt.Println("FAIL " + name)
}
