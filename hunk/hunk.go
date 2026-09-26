// Package hunk groups a shortest edit script into context hunks.
package hunk

import "ontology/edit"

// Line is one hunk line: Kind is ' ', '-' or '+'; Text keeps its terminator.
type Line struct {
	Kind byte
	Text []byte
}

// Hunk is a group of changes with context. AStart/BStart are 0-based.
type Hunk struct {
	AStart, ACount int
	BStart, BCount int
	Lines          []Line
}

// Group turns script s (from a to b) into hunks with ctx context lines.
// Changes separated by at most 2*ctx unchanged lines merge into one hunk
// (see DESIGN.md §2). Within a hunk deletions precede insertions.
func Group(a, b [][]byte, s edit.Script, ctx int) []Hunk {
	n, m := len(a), len(b)
	del := make([]bool, n)
	ins := make([]bool, m)
	for _, op := range s {
		if op.Del {
			del[op.A] = true
		} else {
			ins[op.B] = true
		}
	}
	var hs []Hunk
	i, j := 0, 0
	for i < n || j < m {
		if i < n && !del[i] { // matched context line; skip (j advances too)
			i, j = i+1, j+1
			continue
		}
		aLo, bLo := i, j
		aHi, bHi := i, j
		for {
			for (i < n && del[i]) || (j < m && ins[j]) { // consume changes
				if i < n && del[i] {
					i++
				}
				if j < m && ins[j] {
					j++
				}
			}
			aHi, bHi = i, j
			g := 0 // scan the gap of unchanged lines after the changes
			for i < n && !del[i] && g <= 2*ctx {
				i, j, g = i+1, j+1, g+1
			}
			if g > 2*ctx || (i >= n && j >= m) {
				i, j = aHi, bHi // too far or done: close the group
				break
			}
		}
		hs = append(hs, build(a, b, del, ins, aLo, aHi, bLo, bHi, ctx))
	}
	return hs
}

// build emits one hunk covering the change group plus ctx lines of context.
func build(a, b [][]byte, del, ins []bool, aLo, aHi, bLo, bHi, ctx int) Hunk {
	pre := min(ctx, aLo, bLo)
	post := min(ctx, len(a)-aHi, len(b)-bHi)
	h := Hunk{AStart: aLo - pre, BStart: bLo - pre}
	aEnd, bEnd := aHi+post, bHi+post
	p, q := h.AStart, h.BStart
	for p < aEnd || q < bEnd {
		switch {
		case p < aEnd && del[p]:
			h.Lines = append(h.Lines, Line{'-', a[p]})
			p++
			h.ACount++
		case q < bEnd && ins[q]:
			h.Lines = append(h.Lines, Line{'+', b[q]})
			q++
			h.BCount++
		default: // matched context line
			h.Lines = append(h.Lines, Line{' ', a[p]})
			p, q = p+1, q+1
			h.ACount++
			h.BCount++
		}
	}
	return h
}
