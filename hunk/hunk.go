// Package hunk groups an edit script into unified-diff hunks with a
// configurable number of context lines.
package hunk

import "ontology/edit"

// Line is one line inside a hunk; Kind is ' ', '-' or '+'.
type Line struct {
	Kind byte
	Text []byte
}

// Hunk is one @@ block. Starts are 1-based; a zero Count means the
// Start names the line *before* the insertion point (0 at file head).
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Lines              []Line
}

// Group converts ops into hunks with c context lines per side.
// Changes separated by at most 2*c unchanged lines merge into one
// hunk; a gap of 2*c+1 or more splits them.
func Group(ops []edit.Op, c int) []Hunk {
	n := len(ops)
	oldAt := make([]int, n+1) // 1-based next old/new line before op i
	newAt := make([]int, n+1)
	o, w := 1, 1
	for i, op := range ops {
		oldAt[i], newAt[i] = o, w
		switch op.Kind {
		case ' ':
			o, w = o+1, w+1
		case '-':
			o++
		case '+':
			w++
		}
	}
	oldAt[n], newAt[n] = o, w
	var hunks []Hunk
	for i := 0; i < n; {
		if ops[i].Kind == ' ' {
			i++
			continue
		}
		e, gap := i+1, 0 // group spans [i, e); extend over gaps <= 2c
		for j := e; j < n; j++ {
			if ops[j].Kind == ' ' {
				gap++
				if gap > 2*c {
					break
				}
			} else {
				gap, e = 0, j+1
			}
		}
		lo, hi := i-c, e+c
		if lo < 0 {
			lo = 0
		}
		if hi > n {
			hi = n
		}
		h := Hunk{OldStart: oldAt[lo], NewStart: newAt[lo]}
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
			h.OldStart = oldAt[lo] - 1
		}
		if h.NewCount == 0 {
			h.NewStart = newAt[lo] - 1
		}
		hunks = append(hunks, h)
		i = e
	}
	return hunks
}
