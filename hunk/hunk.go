// Package hunk groups an edit script into hunks with C context lines,
// merging changes separated by at most 2C unchanged lines.
package hunk

import "ontology/edit"

// Line is one line inside a hunk: Kind is ' ', '-' or '+';
// Text is the line content including its terminator (if any).
type Line struct {
	Kind byte
	Text []byte
}

// Hunk is a contiguous group of changes plus surrounding context.
// OldIdx/NewIdx are 0-based start line indices into the old/new files.
type Hunk struct {
	OldIdx, OldCount int
	NewIdx, NewCount int
	Lines            []Line
}

// Diff computes hunks between line sequences a and b with ctx context
// lines. It returns edit.ErrTooBig if the distance exceeds maxDist.
func Diff(a, b [][]byte, ctx, maxDist int) ([]Hunk, error) {
	ops, err := edit.Diff(a, b, maxDist)
	if err != nil {
		return nil, err
	}
	return Build(a, b, ops, ctx), nil
}

// Build groups ops into hunks. Two changes with g unchanged lines
// between them merge iff g <= 2*ctx (see DESIGN.md).
func Build(a, b [][]byte, ops []edit.Op, ctx int) []Hunk {
	var changes []int
	for i, op := range ops {
		if op.Kind != edit.Keep {
			changes = append(changes, i)
		}
	}
	if len(changes) == 0 {
		return nil
	}
	type group struct{ lo, hi int }
	groups := []group{{changes[0], changes[0]}}
	for _, c := range changes[1:] {
		g := &groups[len(groups)-1]
		if c-g.hi-1 <= 2*ctx {
			g.hi = c
		} else {
			groups = append(groups, group{c, c})
		}
	}
	oldPos := make([]int, len(ops)+1)
	newPos := make([]int, len(ops)+1)
	for i, op := range ops {
		oldPos[i+1], newPos[i+1] = oldPos[i], newPos[i]
		switch op.Kind {
		case edit.Keep:
			oldPos[i+1]++
			newPos[i+1]++
		case edit.Del:
			oldPos[i+1]++
		case edit.Ins:
			newPos[i+1]++
		}
	}
	var hunks []Hunk
	for _, g := range groups {
		start := g.lo
		for k := 0; k < ctx && start > 0 && ops[start-1].Kind == edit.Keep; k++ {
			start--
		}
		end := g.hi + 1
		for k := 0; k < ctx && end < len(ops) && ops[end].Kind == edit.Keep; k++ {
			end++
		}
		h := Hunk{OldIdx: oldPos[start], NewIdx: newPos[start]}
		oi, ni := h.OldIdx, h.NewIdx
		for _, op := range ops[start:end] {
			switch op.Kind {
			case edit.Keep:
				h.Lines = append(h.Lines, Line{' ', a[oi]})
				oi++
				ni++
			case edit.Del:
				h.Lines = append(h.Lines, Line{'-', a[oi]})
				oi++
			case edit.Ins:
				h.Lines = append(h.Lines, Line{'+', b[ni]})
				ni++
			}
		}
		h.OldCount = oi - h.OldIdx
		h.NewCount = ni - h.NewIdx
		hunks = append(hunks, h)
	}
	return hunks
}
