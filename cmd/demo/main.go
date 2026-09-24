// Command demo prints OK/FAIL lines for the aligned-snapshot-cut checks.
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"

	"ontology/cut"
	"ontology/snap"
)

var failed bool

func check(name string, err error) {
	if err != nil {
		failed = true
		fmt.Printf("FAIL %s: %v\n", name, err)
		return
	}
	fmt.Printf("OK %s\n", name)
}

// traceStep is one expected row of the NOTES.md eight-step table.
type traceStep struct {
	feed     int
	counts   [3]int
	cut      int
	pending  [3]int
	rejected bool
}

var trace = []traceStep{
	{0, [3]int{1, 0, 0}, 0, [3]int{1, 0, 0}, false},
	{1, [3]int{1, 1, 0}, 0, [3]int{1, 1, 0}, false},
	{2, [3]int{1, 1, 1}, 1, [3]int{0, 0, 0}, false},
	{0, [3]int{2, 1, 1}, 1, [3]int{1, 0, 0}, false},
	{0, [3]int{3, 1, 1}, 1, [3]int{2, 0, 0}, false},
	{0, [3]int{3, 1, 1}, 1, [3]int{2, 0, 0}, true},
	{1, [3]int{3, 2, 1}, 1, [3]int{2, 1, 0}, false},
	{2, [3]int{3, 2, 2}, 2, [3]int{1, 0, 0}, false},
}

// checkTrace replays the eight-step sequence against cut.Tracker and
// verifies C, per-partition counts and pending after every step.
func checkTrace() error {
	tr := cut.New(3, 2)
	for i, st := range trace {
		ok, _ := tr.Feed(st.feed)
		if ok == st.rejected {
			return fmt.Errorf("step %d: rejected=%v, want %v", i+1, !ok, st.rejected)
		}
		if tr.Cut() != st.cut {
			return fmt.Errorf("step %d: C=%d, want %d", i+1, tr.Cut(), st.cut)
		}
		for p := 0; p < 3; p++ {
			if tr.Count(p) != st.counts[p] || tr.Pending(p) != st.pending[p] {
				return fmt.Errorf("step %d p%d: count=%d pending=%d, want %d/%d",
					i+1, p, tr.Count(p), tr.Pending(p), st.counts[p], st.pending[p])
			}
		}
	}
	return nil
}

func main() {
	check("8-step trace: C/pending per step, step 6 rejected", checkTrace())
	if failed {
		os.Exit(1)
	}
}
