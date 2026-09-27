// Package hunk groups a shortest edit script into unified-diff hunks with a
// configurable context size and GNU-compatible merging.
package hunk

import (
	"ontology/edit"
	"ontology/lines"
)

// Item is one body entry: an edit op plus the side-specific "no newline at end
// of file" flags that follow this exact line in the rendered text.
type Item struct {
	Op      edit.Op
	OldNoNL bool
	NewNoNL bool
}

// Hunk is one contiguous @@ block. OldStart/NewStart are 1-based; a zero start
// with a zero count marks the seam before the first line.
type Hunk struct {
	OldStart int
	OldCount int
	NewStart int
	NewCount int
	Items    []Item
	lo       int
	hi       int
}

// Build groups ops into hunks using C lines of context. Change runs separated
// by at most 2C equal lines are merged.
func Build(ops []edit.Op, a, b []lines.Line, c int) []Hunk {
	oldNoNL := func(op edit.Op) bool {
		return op.Kind != edit.Insert && op.Ai == len(a)-1 && len(a) > 0 && len(a[op.Ai].NL) == 0
	}
	newNoNL := func(op edit.Op) bool {
		return op.Kind != edit.Delete && op.Bi == len(b)-1 && len(b) > 0 && len(b[op.Bi].NL) == 0
	}
	items := make([]Item, len(ops))
	for i, op := range ops {
		items[i] = Item{Op: op, OldNoNL: oldNoNL(op), NewNoNL: newNoNL(op)}
	}
	runs := changeRuns(items)
	hs := make([]Hunk, 0, len(runs))
	for _, r := range runs {
		lo, hi := r[0], r[len(r)-1]+1
		lo, hi = grow(items, lo, hi, c)
		hs = append(hs, makeHunk(items, lo, hi))
	}
	merged := []Hunk{}
	for _, h := range hs {
		if len(merged) > 0 && h.lo-merged[len(merged)-1].hi <= 2*c {
			prev := merged[len(merged)-1]
			merged[len(merged)-1] = makeHunk(items, prev.lo, h.hi)
		} else {
			merged = append(merged, h)
		}
	}
	return merged
}

func changeRuns(items []Item) [][]int {
	var runs [][]int
	var cur []int
	for i, it := range items {
		if it.Op.Kind == edit.Equal {
			if cur != nil {
				runs, cur = append(runs, cur), nil
			}
			continue
		}
		cur = append(cur, i)
	}
	if cur != nil {
		runs = append(runs, cur)
	}
	return runs
}

func grow(items []Item, lo, hi, c int) (int, int) {
	for k := 0; k < c && lo > 0 && items[lo-1].Op.Kind == edit.Equal; k++ {
		lo--
	}
	for k := 0; k < c && hi < len(items) && items[hi].Op.Kind == edit.Equal; k++ {
		hi++
	}
	return lo, hi
}

func makeHunk(items []Item, lo, hi int) Hunk {
	oldN, newN := 0, 0
	for j := lo; j < hi; j++ {
		if items[j].Op.Kind != edit.Insert {
			oldN++
		}
		if items[j].Op.Kind != edit.Delete {
			newN++
		}
	}
	oldStart, newStart := 0, 0
	if oldN > 0 {
		for j := lo; j < hi; j++ {
			if items[j].Op.Kind != edit.Insert {
				oldStart = items[j].Op.Ai + 1
				break
			}
		}
	} else if s := seamIndex(items, lo, false); s >= 0 {
		oldStart = s + 1
	}
	if newN > 0 {
		for j := lo; j < hi; j++ {
			if items[j].Op.Kind != edit.Delete {
				newStart = items[j].Op.Bi + 1
				break
			}
		}
	} else if s := seamIndex(items, lo, true); s >= 0 {
		newStart = s + 1
	}
	return Hunk{oldStart, oldN, newStart, newN, append([]Item(nil), items[lo:hi]...), lo, hi}
}

// seamIndex returns the index of the last line on one side immediately before
// the seam at script position lo, or -1 when the seam is before line zero.
func seamIndex(items []Item, lo int, useNew bool) int {
	for j := lo - 1; j >= 0; j-- {
		if useNew && items[j].Op.Kind != edit.Delete {
			return items[j].Op.Bi
		}
		if !useNew && items[j].Op.Kind != edit.Insert {
			return items[j].Op.Ai
		}
	}
	return -1
}
