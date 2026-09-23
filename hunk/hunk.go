// Package hunk groups an edit script into hunks with C context lines.
// Two changes merge into one hunk when the gap of unchanged lines between
// them is <= 2*C (see DESIGN.md).
package hunk

import (
	"ontology/edit"
	"ontology/lines"
)

// Entry is one line inside a hunk.
type Entry struct {
	Kind edit.OpKind // Equal (context), Del or Ins
	Line lines.Line
}

// Hunk is one @@ block. Start lines follow the unified rule: when a count
// is zero the start is the line number before the insertion point.
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Entries            []Entry
}

type posOp struct {
	op           edit.Op
	oldAt, newAt int // 1-based counters before the op
}

// Build groups ops into hunks with the given number of context lines.
func Build(ops []edit.Op, context int) []Hunk {
	ann := make([]posOp, len(ops))
	o, n := 1, 1
	for i, op := range ops {
		ann[i] = posOp{op: op, oldAt: o, newAt: n}
		switch op.Kind {
		case edit.Equal:
			o++
			n++
		case edit.Del:
			o++
		case edit.Ins:
			n++
		}
	}
	var runs [][2]int
	for i := 0; i < len(ann); {
		if ann[i].op.Kind == edit.Equal {
			i++
			continue
		}
		j := i
		for j < len(ann) && ann[j].op.Kind != edit.Equal {
			j++
		}
		if k := len(runs) - 1; k >= 0 && i-runs[k][1] <= 2*context {
			runs[k][1] = j // gap of equal lines small enough: merge
		} else {
			runs = append(runs, [2]int{i, j})
		}
		i = j
	}
	var out []Hunk
	for _, r := range runs {
		lo, hi := r[0]-context, r[1]+context
		if lo < 0 {
			lo = 0
		}
		if hi > len(ann) {
			hi = len(ann)
		}
		out = append(out, makeHunk(ann[lo:hi]))
	}
	return out
}

func makeHunk(win []posOp) Hunk {
	var h Hunk
	oldSeen, newSeen := false, false
	for _, p := range win {
		h.Entries = append(h.Entries, Entry{Kind: p.op.Kind, Line: p.op.Line})
		switch p.op.Kind {
		case edit.Equal:
			h.OldCount++
			h.NewCount++
			oldSeen, newSeen = true, true
			if h.OldCount == 1 {
				h.OldStart = p.oldAt
			}
			if h.NewCount == 1 {
				h.NewStart = p.newAt
			}
		case edit.Del:
			h.OldCount++
			if !oldSeen {
				oldSeen = true
				h.OldStart = p.oldAt
			}
		case edit.Ins:
			h.NewCount++
			if !newSeen {
				newSeen = true
				h.NewStart = p.newAt
			}
		}
	}
	if h.OldCount == 0 {
		h.OldStart = win[0].oldAt - 1 // line before the insertion point
	}
	if h.NewCount == 0 {
		h.NewStart = win[0].newAt - 1
	}
	return h
}
