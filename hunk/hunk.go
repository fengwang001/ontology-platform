// Package hunk groups an edit script into hunks with C context lines,
// merging nearby changes the way GNU diff -U C does.
package hunk

import "ontology/edit"

// Line is one rendered line of a hunk: Kind ' ' (context), '-' or '+'.
// Text keeps its original terminator (or lacks it for a final line
// without newline).
type Line struct {
	Kind byte
	Text string
}

// Hunk is a group of changes with surrounding context. OldStart/NewStart
// are 0-based indices into the old/new line sequences (the number of
// lines before the hunk); OldCount/NewCount are the covered line counts.
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Lines              []Line
}

// OldLines returns the hunk's context and deletion lines in order; it is
// the exact byte sequence the hunk expects at its old-side position.
func (h Hunk) OldLines() []string {
	var out []string
	for _, l := range h.Lines {
		if l.Kind != '+' {
			out = append(out, l.Text)
		}
	}
	return out
}

// Build groups ops into hunks with ctx context lines. Two changes
// separated by g unchanged lines merge iff g <= 2*ctx.
func Build(a, b []string, ops []edit.Op, ctx int) []Hunk {
	var hunks []Hunk
	i := 0
	for i < len(ops) {
		if ops[i].Kind == ' ' {
			i++
			continue
		}
		start := i
		for start > 0 && ops[start-1].Kind == ' ' && i-start < ctx {
			start--
		}
		end := i // exclusive; extended below
		gap := 0
		for j := i; j < len(ops); j++ {
			if ops[j].Kind == ' ' {
				gap++
				continue
			}
			if gap > 2*ctx {
				break
			}
			end = j + 1
			gap = 0
		}
		trail := 0
		for end+trail < len(ops) && ops[end+trail].Kind == ' ' && trail < ctx {
			trail++
		}
		h := Hunk{OldStart: ops[start].A, NewStart: ops[start].B}
		for _, op := range ops[start : end+trail] {
			switch op.Kind {
			case ' ':
				h.Lines = append(h.Lines, Line{' ', a[op.A]})
				h.OldCount++
				h.NewCount++
			case '-':
				h.Lines = append(h.Lines, Line{'-', a[op.A]})
				h.OldCount++
			case '+':
				h.Lines = append(h.Lines, Line{'+', b[op.B]})
				h.NewCount++
			}
		}
		hunks = append(hunks, h)
		i = end + trail
	}
	return hunks
}
