// Package hunk groups an edit script into hunks with C context lines,
// merging changes separated by at most 2C unchanged lines (GNU rule,
// see DESIGN.md).
package hunk

import "ontology/edit"

// Line is one line of a hunk body: Kind is ' ', '-' or '+'.
type Line struct {
	Kind byte
	Text []byte
}

// Hunk describes one @@ -AStart,ACount +BStart,BCount @@ block.
// Starts follow the GNU convention: when the count is zero the start
// is the number of lines before the insertion point, otherwise the
// 1-based number of the first line.
type Hunk struct {
	AStart, ACount int
	BStart, BCount int
	Lines          []Line
}

// Group splits s into hunks with ctx context lines on each side.
func Group(s edit.Script, a, b [][]byte, ctx int) []Hunk {
	var chg []int
	for i, op := range s {
		if op.Kind != ' ' {
			chg = append(chg, i)
		}
	}
	if len(chg) == 0 {
		return nil
	}
	var hs []Hunk
	lo := chg[0]
	for i := 1; i <= len(chg); i++ {
		if i < len(chg) && chg[i]-chg[i-1]-1 <= 2*ctx {
			continue // gap of unchanged lines fits in both contexts: merge
		}
		hs = append(hs, build(s, a, b, lo, chg[i-1], ctx))
		if i < len(chg) {
			lo = chg[i]
		}
	}
	return hs
}

func build(s edit.Script, a, b [][]byte, lo, hi, ctx int) Hunk {
	if lo -= ctx; lo < 0 {
		lo = 0
	}
	if hi += ctx; hi > len(s)-1 {
		hi = len(s) - 1
	}
	var h Hunk
	aBefore, bBefore := 0, 0
	for _, op := range s[:lo] {
		if op.Kind != '+' {
			aBefore++
		}
		if op.Kind != '-' {
			bBefore++
		}
	}
	for _, op := range s[lo : hi+1] {
		var text []byte
		if op.Kind == '+' {
			text = b[op.B]
		} else {
			text = a[op.A]
			h.ACount++
		}
		if op.Kind != '-' {
			h.BCount++
		}
		h.Lines = append(h.Lines, Line{Kind: op.Kind, Text: text})
	}
	h.AStart, h.BStart = aBefore, bBefore
	if h.ACount > 0 {
		h.AStart = aBefore + 1
	}
	if h.BCount > 0 {
		h.BStart = bBefore + 1
	}
	return h
}
