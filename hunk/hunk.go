// Package hunk groups an edit script into unified-diff hunks with C lines of
// context, merging neighboring hunks exactly as GNU "diff -U C" does.
package hunk

import "ontology/edit"

// Item is one body line of a hunk, already carrying the original line data
// (including terminator) from the side it belongs to.
type Item struct {
	Kind edit.Kind
	Data []byte
}

// Hunk is one unified-diff hunk. OldStart/NewStart follow the unified-format
// convention including the zero-count rules of DESIGN.md §1: when a side has
// zero lines its start is the number of lines preceding the insertion point.
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Items              []Item
}

// Group splits ops into hunks with c lines of context. Two change groups with
// g unchanged lines between merge iff g <= 2*c (DESIGN.md §2).
func Group(ops []edit.Op, c int) []Hunk {
	if c < 0 {
		c = 0
	}
	type chg struct{ lo, hi int } // change op-index span [lo,hi)
	var groups []chg
	for i := 0; i < len(ops); {
		if ops[i].Kind == edit.Equal {
			i++
			continue
		}
		j := i
		for j < len(ops) && ops[j].Kind != edit.Equal {
			j++
		}
		groups = append(groups, chg{i, j})
		i = j
	}
	var out []Hunk
	for gi := 0; gi < len(groups); gi++ {
		lo, hi := groups[gi].lo, groups[gi].hi
		for gi+1 < len(groups) {
			gap := groups[gi+1].lo - hi
			if gap > 2*c {
				break
			}
			hi = groups[gi+1].hi
			gi++
		}
		from := lo - c
		if from < 0 {
			from = 0
		}
		to := hi + c
		if to > len(ops) {
			to = len(ops)
		}
		out = append(out, build(ops[from:to]))
	}
	return out
}

func build(seg []edit.Op) Hunk {
	h := Hunk{}
	oldBefore, newBefore := 0, 0
	if n := leadingEqual(seg); n > 0 {
		oldBefore, newBefore = n, n
	}
	for _, op := range seg {
		var data []byte
		switch op.Kind {
		case edit.Equal:
			data = op.OldLine.Data
			h.OldCount++
			h.NewCount++
		case edit.Delete:
			data = op.OldLine.Data
			h.OldCount++
		case edit.Insert:
			data = op.NewLine.Data
			h.NewCount++
		}
		h.Items = append(h.Items, Item{Kind: op.Kind, Data: data})
	}
	h.OldStart = startNumber(oldBefore, h.OldCount)
	h.NewStart = startNumber(newBefore, h.NewCount)
	return h
}

// startNumber maps (lines-before-first-body-line, body-line count) to the
// number printed in a hunk header (DESIGN.md §1).
func startNumber(before, count int) int {
	if count == 0 {
		return before
	}
	return before + 1
}

func leadingEqual(seg []edit.Op) int {
	n := 0
	for _, op := range seg {
		if op.Kind != edit.Equal {
			break
		}
		n++
	}
	return n
}
