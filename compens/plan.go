// Package compens computes the compensation execution order from the set of
// steps that produced (or may have produced) an effect. It depends only on
// the step package.
package compens

import (
	"sort"

	"ontology/step"
)

// Item is one step due for compensation, emitted in strict reverse order.
type Item struct {
	Index   int
	Step    step.Step
}

// Plan is the reverse-ordered list of steps that must be compensated.
type Plan struct {
	Items []Item
}

// Build constructs a plan from the ordered steps and the set of step indexes
// that succeeded (including uncertain / possibly-applied outcomes). Failed
// and never-executed indexes are simply absent from succeeded and are never
// compensated.
func Build(steps []step.Step, succeeded map[int]bool) Plan {
	idx := make([]int, 0, len(succeeded))
	for i, ok := range succeeded {
		if !ok || i < 0 || i >= len(steps) {
			continue
		}
		idx = append(idx, i)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(idx)))
	p := Plan{Items: make([]Item, 0, len(idx))}
	for _, i := range idx {
		p.Items = append(p.Items, Item{Index: i, Step: steps[i]})
	}
	return p
}

// Pending returns the reverse-ordered plan limited to indexes <= upTo,
// skipping indexes already present in done. It is used both to start
// compensation and to resume after a crash.
func Pending(steps []step.Step, succeeded map[int]bool, upTo int, done map[int]bool) Plan {
	base := Build(steps, succeeded)
	out := Plan{Items: make([]Item, 0, len(base.Items))}
	for _, it := range base.Items {
		if it.Index <= upTo && !done[it.Index] {
			out.Items = append(out.Items, it)
		}
	}
	return out
}
