// Package hunk groups a shortest edit script into unified-diff hunks with a
// given number C of context lines. Adjacent change groups merge when the gap
// of unchanged lines between them is at most 2C.
package hunk

import (
	"ontology/edit"
	"ontology/lines"
)

// Row is one line inside a hunk.
type Row struct {
	Kind edit.Kind
	Old  lines.Line
	New  lines.Line
}

// Hunk is one contiguous hunk. OldStart/NewStart follow GNU unified-diff
// numbering: for a zero count the start equals the preceding line number
// (0 when the gap is at the very beginning).
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Rows               []Row
}

// Build groups ops into hunks using C lines of context.
func Build(ops []edit.Op, c int) []Hunk {
	if c < 0 {
		c = 0
	}
	changes := make([]int, 0)
	for i, op := range ops {
		if op.Kind != edit.Equal {
			changes = append(changes, i)
		}
	}
	if len(changes) == 0 {
		return nil
	}
	type span struct{ lo, hi int } // half-open op index range
	groups := make([]span, 0)
	lo, hi := max(changes[0]-c, 0), min(changes[0]+c+1, len(ops))
	for _, idx := range changes[1:] {
		glo, ghi := max(idx-c, 0), min(idx+c+1, len(ops))
		if glo <= hi { // gap of equal ops <= 2C: merge
			hi = ghi
			continue
		}
		groups = append(groups, span{lo, hi})
		lo, hi = glo, ghi
	}
	groups = append(groups, span{lo, hi})

	// Line index of each op boundary: index (0-based) of the old/new line the
	// op consumes, counted by walking ops.
	oldIdx := make([]int, len(ops)+1)
	newIdx := make([]int, len(ops)+1)
	ox, nx := 0, 0
	for i, op := range ops {
		oldIdx[i], newIdx[i] = ox, nx
		if op.Kind != edit.Insert {
			ox++
		}
		if op.Kind != edit.Delete {
			nx++
		}
	}
	oldIdx[len(ops)], newIdx[len(ops)] = ox, nx

	hunks := make([]Hunk, 0, len(groups))
	for _, g := range groups {
		oc, nc := 0, 0
		rows := make([]Row, 0, g.hi-g.lo)
		for _, op := range ops[g.lo:g.hi] {
			rows = append(rows, Row{Kind: op.Kind, Old: op.Old, New: op.New})
			if op.Kind != edit.Insert {
				oc++
			}
			if op.Kind != edit.Delete {
				nc++
			}
		}
		os, ns := oldIdx[g.lo]+1, newIdx[g.lo]+1
		if oc == 0 {
			os = oldIdx[g.lo] // preceding old line, 0 at file start
		}
		if nc == 0 {
			ns = newIdx[g.lo]
		}
		hunks = append(hunks, Hunk{
			OldStart: os, OldCount: oc,
			NewStart: ns, NewCount: nc, Rows: rows,
		})
	}
	return hunks
}
