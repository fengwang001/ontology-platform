// Package hunk groups an edit script into hunks with C context lines,
// merging changes whose gap of unchanged lines is at most 2C.
package hunk

import "ontology/edit"

// Line is one hunk body line; Kind is ' ', '-' or '+'.
type Line struct {
	Kind byte
	Text string
}

// Hunk is one @@ section. Starts are 1-based, except that a zero Count
// stores the line number of the preceding line (GNU diff convention).
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Lines              []Line
}

// Build groups ops into hunks with ctx context lines on each side.
func Build(ops []edit.Op, ctx int) []Hunk {
	var hs []Hunk
	oldSeen, newSeen, i := 0, 0, 0
	adv := func(t int) {
		for ; i < t; i++ {
			if ops[i].Kind != '+' {
				oldSeen++
			}
			if ops[i].Kind != '-' {
				newSeen++
			}
		}
	}
	for i < len(ops) {
		if ops[i].Kind == ' ' {
			adv(i + 1)
			continue
		}
		start, sOld, sNew := i, oldSeen, newSeen
		end, gap := i, 0
		for j := i; j < len(ops); j++ {
			if ops[j].Kind == ' ' {
				gap++
				if gap > 2*ctx {
					break
				}
			} else {
				gap, end = 0, j
			}
		}
		lo := start - ctx
		if lo < 0 {
			lo = 0
		}
		sOld -= start - lo
		sNew -= start - lo
		hi := end + 1 + ctx
		if hi > len(ops) {
			hi = len(ops)
		}
		h := Hunk{OldStart: sOld + 1, NewStart: sNew + 1}
		for _, op := range ops[lo:hi] {
			h.Lines = append(h.Lines, Line{op.Kind, op.Line})
			if op.Kind != '+' {
				h.OldCount++
			}
			if op.Kind != '-' {
				h.NewCount++
			}
		}
		if h.OldCount == 0 {
			h.OldStart--
		}
		if h.NewCount == 0 {
			h.NewStart--
		}
		hs = append(hs, h)
		adv(hi)
	}
	return hs
}
