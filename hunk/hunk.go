// Package hunk groups an edit script into unified-diff hunks with a fixed
// number of context lines.
package hunk

import "ontology/edit"

// Hunk is one contiguous section of an edit script including context.
type Hunk struct {
	// Edits are the aligned edits (Equal/Delete/Insert) in script order.
	Edits []edit.Edit
	// OldStart/NewStart are 1-based starting lines; 0 means an insertion
	// before the first line.
	OldStart, NewStart int
	// OldCount/NewCount are the hunk line counts on each side.
	OldCount, NewCount int
	// OldNoNL/NewNoNL mark that the last emitted side line has no newline.
	OldNoNL, NewNoNL bool
}

// Group splits es into hunks with c context lines. Two change regions with g
// separating equal lines are merged while g <= 2*c and split when g > 2*c.
func Group(es []edit.Edit, c int) []Hunk {
	var changes [][2]int
	for i, e := range es {
		if e.Op != edit.Equal {
			if len(changes) == 0 || i > changes[len(changes)-1][1]+1 {
				changes = append(changes, [2]int{i, i})
			} else {
				changes[len(changes)-1][1] = i
			}
		}
	}
	var regions [][2]int
	for _, ch := range changes {
		if len(regions) == 0 {
			regions = append(regions, ch)
			continue
		}
		prev := regions[len(regions)-1]
		gap := ch[0] - prev[1] - 1
		if gap <= 2*c {
			prev[1] = ch[1]
			regions[len(regions)-1] = prev
		} else {
			regions = append(regions, ch)
		}
	}
	hs := make([]Hunk, 0, len(regions))
	for _, r := range regions {
		lo := r[0] - c
		if lo < 0 {
			lo = 0
		}
		hi := r[1] + 1 + c
		if hi > len(es) {
			hi = len(es)
		}
		h := Hunk{Edits: es[lo:hi]}
		oldIdx, newIdx := 0, 0
		for _, e := range es[:lo] {
			switch e.Op {
			case edit.Equal:
				oldIdx++
				newIdx++
			case edit.Delete:
				oldIdx++
			case edit.Insert:
				newIdx++
			}
		}
		h.OldStart, h.NewStart = oldIdx, newIdx
		for _, e := range h.Edits {
			if e.Op == edit.Equal || e.Op == edit.Delete {
				h.OldCount++
			}
			if e.Op == edit.Equal || e.Op == edit.Insert {
				h.NewCount++
			}
		}
		// Convert 0-based index before the first hunk line into GNU's
		// 1-based start: a positive count starts at index+1; a zero count
		// keeps the index of the preceding line (0 at file start).
		if h.OldCount > 0 {
			h.OldStart++
		}
		if h.NewCount > 0 {
			h.NewStart++
		}
		h.OldNoNL = lastNoNL(h.Edits, false)
		h.NewNoNL = lastNoNL(h.Edits, true)
		hs = append(hs, h)
	}
	return hs
}

// lastNoNL reports whether the last line emitted on one side ends without a
// newline marker (EOL is nil).
func lastNoNL(es []edit.Edit, sideNew bool) bool {
	for i := len(es) - 1; i >= 0; i-- {
		e := es[i]
		if sideNew && e.Op != edit.Delete {
			return len(e.B.EOL) == 0
		}
		if !sideNew && e.Op != edit.Insert {
			return len(e.A.EOL) == 0
		}
	}
	return false
}
