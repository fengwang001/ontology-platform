package main

import (
	"os"

	"ontology/pipeline"
)

// maybeCrashChild runs the in-process crash harness when the demo was
// re-executed with ESORT_CHILD=1. It never returns.
func maybeCrashChild() {
	if os.Getenv("ESORT_CHILD") != "1" {
		return
	}
	dir := os.Getenv("ESORT_CHILD_DIR")
	phase := os.Getenv("ESORT_CHILD_PHASE")
	faults := &pipeline.Faults{CrashAfter: func(p string) bool {
		if p == phase || (phase == "merge" && p == "merge-mid") {
			os.Exit(77)
		}
		return false
	}}
	q, err := openSort(dir, faults)
	must(err)
	fill(q, 500)
	must(q.Close())
	os.Exit(0)
}
