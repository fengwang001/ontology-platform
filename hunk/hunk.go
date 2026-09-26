// Package hunk groups a shortest edit script into unified-diff hunks with a
// configurable context size. Two change groups merge iff the unchanged gap g
// satisfies g <= 2*context.
package hunk

import "ontology/edit"

// Hunk is one contiguous @@ block.
type Hunk struct {
	OldStart int // 1-based first old line; for OldCount 0: preceding old line (may be 0)
	OldCount int
	NewStart int
	NewCount int
	Steps    []edit.Step // ordered; includes context equals only
}

// Build groups script into hunks. nil steps means identical texts.
func Build(steps []edit.Step, context int) []Hunk {
	if context < 0 {
		context = 0
	}
	var changed []int
	for i, s := range steps {
		if s.Op != edit.Equal {
			changed = append(changed, i)
		}
	}
	if len(changed) == 0 {
		return nil
	}
	type group struct{ first, last int }
	groups := []group{{changed[0], changed[0]}}
	for _, ci := range changed[1:] {
		g := &groups[len(groups)-1]
		if ci-g.last-1 <= 2*context {
			g.last = ci
		} else {
			groups = append(groups, group{ci, ci})
		}
	}
	// consumed old/new line counts before each step index
	oldBefore := make([]int, len(steps)+1)
	newBefore := make([]int, len(steps)+1)
	oc, nc := 0, 0
	for i, s := range steps {
		oldBefore[i] = oc
		newBefore[i] = nc
		if s.Old != nil {
			oc++
		}
		if s.New != nil {
			nc++
		}
	}
	oldBefore[len(steps)] = oc
	newBefore[len(steps)] = nc

	var out []Hunk
	for _, grp := range groups {
		lo := grp.first - context
		if lo < 0 {
			lo = 0
		}
		hi := grp.last + context + 1
		if hi > len(steps) {
			hi = len(steps)
		}
		h := Hunk{Steps: steps[lo:hi]}
		for _, s := range h.Steps {
			if s.Old != nil {
				h.OldCount++
			}
			if s.New != nil {
				h.NewCount++
			}
		}
		if h.OldCount == 0 {
			h.OldStart = oldBefore[lo]
		} else {
			h.OldStart = oldBefore[lo] + 1
		}
		if h.NewCount == 0 {
			h.NewStart = newBefore[lo]
		} else {
			h.NewStart = newBefore[lo] + 1
		}
		out = append(out, h)
	}
	return out
}
