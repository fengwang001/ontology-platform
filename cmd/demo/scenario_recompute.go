package main

import (
	"fmt"
	"path/filepath"

	"ontology/agg"
	"ontology/view"
)

// scenarioRecompute inserts 100k records and performs 5000 deletions of
// which exactly three remove the current group minimum. It returns the
// observed Min and Sum recomputation counters.
func scenarioRecompute(tmp string) (minN, sumN int64) {
	path := filepath.Join(tmp, "recompute")
	v := openView(path, view.Options{})
	defer v.Close()

	const n = 100_000
	var ver uint64
	next := func() uint64 { ver++; return ver }

	for i := 0; i < n; i++ {
		must(v.Submit(ins(next(), fmt.Sprintf("k%06d", i), "g", float64(i+1))))
	}
	for _, i := range []int{0, 1, 2} {
		must(v.Submit(del(next(), fmt.Sprintf("k%06d", i), "g", float64(i+1))))
	}
	done := 3
	for i := 1000; done < 5000; i++ {
		must(v.Submit(del(next(), fmt.Sprintf("k%06d", i), "g", float64(i+1))))
		done++
	}
	s := v.Stats()
	return s.TriggersByKind[agg.Min], s.TriggersByKind[agg.Sum]
}
