// Package hunk groups an edit script into context hunks.
package hunk

import "ontology/edit"

// Hunk is one contiguous group of script ops. AStart/ACount describe
// the old text, BStart/BCount the new text. Starts are 1-based; when a
// count is 0 the start is the number of lines consumed before the hunk
// (see DESIGN.md).
type Hunk struct {
	Ops                            []edit.Op
	AStart, ACount, BStart, BCount int
}

// Group splits ops into hunks, keeping at most ctx unchanged lines on
// each side. Two change groups separated by g unchanged lines merge
// exactly when g <= 2*ctx (see DESIGN.md).
func Group(ops []edit.Op, ctx int) []Hunk {
	var hs []Hunk
	n := len(ops)
	pos := 0
	aLine, bLine := 0, 0
	advance := func(to int) {
		for ; pos < to; pos++ {
			if ops[pos].Kind != '+' {
				aLine++
			}
			if ops[pos].Kind != '-' {
				bLine++
			}
		}
	}
	for pos < n {
		j := pos
		for j < n && ops[j].Kind == ' ' {
			j++
		}
		if j == n {
			break
		}
		s := j - ctx
		if s < pos {
			s = pos
		}
		e := j
		for e < n {
			k := e
			for k < n && ops[k].Kind != ' ' {
				k++
			}
			g := 0
			for k+g < n && ops[k+g].Kind == ' ' {
				g++
			}
			e = k + g
			if k+g == n || g > 2*ctx {
				if g > ctx {
					e = k + ctx
				}
				break
			}
		}
		advance(s)
		h := Hunk{Ops: ops[s:e], AStart: aLine + 1, BStart: bLine + 1}
		for _, op := range h.Ops {
			if op.Kind != '+' {
				h.ACount++
			}
			if op.Kind != '-' {
				h.BCount++
			}
		}
		if h.ACount == 0 {
			h.AStart = aLine
		}
		if h.BCount == 0 {
			h.BStart = bLine
		}
		hs = append(hs, h)
		advance(e)
	}
	return hs
}
