// Package hunk groups an edit script into hunks with a configurable
// number of context lines.
package hunk

import "ontology/edit"

// Line is one line inside a hunk; Kind is ' ', '-' or '+'.
type Line struct {
	Kind byte
	Text []byte
}

// Hunk is a group of nearby changes plus surrounding context.
// Starts are 1-based line numbers; when a count is 0 the start is the
// number of the line preceding the insertion point (see DESIGN.md).
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Lines              []Line
}

// Group splits ops into hunks with ctx lines of context around each
// change. Two changes separated by at most 2*ctx unchanged lines merge
// into one hunk; a larger gap splits them.
func Group(a, b [][]byte, ops []edit.Op, ctx int) []Hunk {
	var hs []Hunk
	for i := 0; i < len(ops); {
		for i < len(ops) && ops[i] == edit.OpKeep {
			i++
		}
		if i == len(ops) {
			break
		}
		lo := max(i-ctx, 0)
		last := i
		for j := i + 1; j < len(ops); {
			if ops[j] != edit.OpKeep {
				last = j
				j++
				continue
			}
			k := j
			for k < len(ops) && ops[k] == edit.OpKeep {
				k++
			}
			if k == len(ops) || k-j > 2*ctx {
				break
			}
			j = k
		}
		hi := min(last+1+ctx, len(ops))
		hs = append(hs, build(a, b, ops, lo, hi))
		i = hi
	}
	return hs
}

func build(a, b [][]byte, ops []edit.Op, lo, hi int) Hunk {
	ai, bi := 0, 0
	for _, o := range ops[:lo] {
		switch o {
		case edit.OpKeep:
			ai++
			bi++
		case edit.OpDel:
			ai++
		default:
			bi++
		}
	}
	h := Hunk{OldStart: ai + 1, NewStart: bi + 1}
	for _, o := range ops[lo:hi] {
		switch o {
		case edit.OpKeep:
			h.Lines = append(h.Lines, Line{' ', a[ai]})
			ai++
			bi++
			h.OldCount++
			h.NewCount++
		case edit.OpDel:
			h.Lines = append(h.Lines, Line{'-', a[ai]})
			ai++
			h.OldCount++
		default:
			h.Lines = append(h.Lines, Line{'+', b[bi]})
			bi++
			h.NewCount++
		}
	}
	if h.OldCount == 0 {
		h.OldStart--
	}
	if h.NewCount == 0 {
		h.NewStart--
	}
	return h
}
