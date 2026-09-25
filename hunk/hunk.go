// Package hunk groups an edit script into hunks with C context lines.
// Two change runs separated by g unchanged lines merge iff g <= 2*C
// (see DESIGN.md).
package hunk

import (
	"ontology/edit"
	"ontology/lines"
)

// Line is one line inside a hunk: its kind plus content (terminator kept).
type Line struct {
	Kind byte // ' ', '-' or '+'
	Text lines.Line
}

// Hunk is a group of changes with surrounding context. OldStart/NewStart
// are 0-based indices into the old/new line sequences.
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Lines              []Line
}

// Group splits ops into hunks with ctx context lines around each change.
func Group(ops []edit.Op, a, b []lines.Line, ctx int) []Hunk {
	var ch []int // indices of change ops
	for i, op := range ops {
		if op.Kind != ' ' {
			ch = append(ch, i)
		}
	}
	if len(ch) == 0 {
		return nil
	}
	var hs []Hunk
	for lo, hi := 0, 0; lo < len(ch); lo = hi {
		hi = lo + 1
		for hi < len(ch) && ch[hi]-ch[hi-1]-1 <= 2*ctx {
			hi++
		}
		from, to := ch[lo], ch[hi-1]+1
		for from > 0 && ops[from-1].Kind == ' ' && ch[lo]-from < ctx {
			from--
		}
		for to < len(ops) && ops[to].Kind == ' ' && to-ch[hi-1]-1 < ctx {
			to++
		}
		h := Hunk{OldStart: ops[from].Old, NewStart: ops[from].New}
		for _, op := range ops[from:to] {
			switch op.Kind {
			case ' ':
				h.OldCount++
				h.NewCount++
				h.Lines = append(h.Lines, Line{' ', a[op.Old]})
			case '-':
				h.OldCount++
				h.Lines = append(h.Lines, Line{'-', a[op.Old]})
			case '+':
				h.NewCount++
				h.Lines = append(h.Lines, Line{'+', b[op.New]})
			}
		}
		hs = append(hs, h)
	}
	return hs
}
