// Package hunk groups an edit script into unified-diff hunks with C lines of
// context, merging adjacent change blocks whenever their gap is at most 2C.
package hunk

import "ontology/edit"

// Line is one rendered hunk row. OldNoNL/NewNoNL report that the
// corresponding final file line carries no terminator (NoNL marker follows).
type Line struct {
	K                edit.Kind
	Op               edit.Op
	OldNoNL, NewNoNL bool
}

// Hunk is one contiguous block with 1-based start coordinates and counts.
// A zero count means the hunk starts after Start-1 lines (see DESIGN.md 1).
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Lines              []Line
}

// Build groups ops. Context C may be 0. Change blocks separated by g<=2C
// equal lines are merged (see DESIGN.md 2).
func Build(ops []edit.Op, C int) []Hunk {
	// locate maximal runs of non-Equal ops
	var runs [][2]int
	for i := 0; i < len(ops); {
		if ops[i].Kind == edit.Equal {
			i++
			continue
		}
		s := i
		for i < len(ops) && ops[i].Kind != edit.Equal {
			i++
		}
		runs = append(runs, [2]int{s, i})
	}
	if len(runs) == 0 {
		return nil
	}
	// expand runs by C on each side, merge when expanded ranges meet (g<=2C)
	groups := [][2]int{}
	for _, r := range runs {
		s, e := max(0, r[0]-C), min(len(ops), r[1]+C)
		if len(groups) > 0 && s <= groups[len(groups)-1][1] {
			groups[len(groups)-1][1] = e
		} else {
			groups = append(groups, [2]int{s, e})
		}
	}
	hs := make([]Hunk, 0, len(groups))
	for _, g := range groups {
		hs = append(hs, makeHunk(ops, g[0], g[1]))
	}
	return hs
}

func makeHunk(ops []edit.Op, s, e int) Hunk {
	h := Hunk{}
	// coordinates: count A/B lines before s, accounting for zero-count case
	var preA, preB int
	for _, op := range ops[:s] {
		switch op.Kind {
		case edit.Equal:
			preA, preB = preA+1, preB+1
		case edit.Delete:
			preA++
		case edit.Insert:
			preB++
		}
	}
	hasOld, hasNew := false, false
	for i := s; i < e; i++ {
		op := ops[i]
		last := i == len(ops)-1
		ln := Line{K: op.Kind, Op: op}
		if last {
			noOld := op.Kind != edit.Insert && len(op.A.EOL) == 0
			noNew := op.Kind != edit.Delete && len(op.B.EOL) == 0
			ln.OldNoNL, ln.NewNoNL = noOld, noNew
		}
		h.Lines = append(h.Lines, ln)
		switch op.Kind {
		case edit.Equal:
			h.OldCount, h.NewCount = h.OldCount+1, h.NewCount+1
			hasOld, hasNew = true, true
		case edit.Delete:
			h.OldCount++
			hasOld = true
		case edit.Insert:
			h.NewCount++
			hasNew = true
		}
	}
	if hasOld {
		h.OldStart = preA + 1
	} else {
		h.OldStart = preA // zero-count: previous line number (may be 0)
	}
	if hasNew {
		h.NewStart = preB + 1
	} else {
		h.NewStart = preB
	}
	return h
}
