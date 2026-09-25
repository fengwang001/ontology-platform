// Package hunk groups an edit script into hunks with C lines of context.
// Adjacent changes separated by at most 2*C unchanged lines are merged.
package hunk

import "ontology/edit"

// Line is one line of a hunk body: Kind is ' ', '-' or '+', Text is the
// line content including its original terminator (if any).
type Line struct {
	Kind byte
	Text []byte
}

// Hunk describes one contiguous change region. OldStart/OldCount refer to
// the old file, NewStart/NewCount to the new file. When a count is 0 the
// start is the line number of the line preceding the insertion point
// (0 at the beginning of the file); otherwise it is the 1-based first line.
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Lines              []Line
}

// Group turns ops (produced by edit.Diff for a -> b) into hunks with ctx
// lines of context. Changes separated by at most 2*ctx unchanged lines are
// merged into one hunk.
func Group(ops []edit.Op, a, b [][]byte, ctx int) []Hunk {
	var hunks []Hunk
	n := len(ops)
	oldIdx, newIdx := 0, 0
	for i := 0; i < n; {
		if ops[i].Kind == ' ' {
			oldIdx++
			newIdx++
			i++
			continue
		}
		lo := i - ctx
		if lo < 0 {
			lo = 0
		}
		end := i
		for j := i; j < n; j++ {
			if ops[j].Kind != ' ' {
				end = j + 1
				continue
			}
			gap := 0
			for j < n && ops[j].Kind == ' ' {
				gap++
				j++
			}
			if gap > 2*ctx || j >= n {
				break
			}
			j--
		}
		hi := end + ctx
		if hi > n {
			hi = n
		}
		h := build(ops[lo:hi], a, b)
		h.OldStart = start(oldIdx-(i-lo), h.OldCount)
		h.NewStart = start(newIdx-(i-lo), h.NewCount)
		hunks = append(hunks, h)
		for i < hi {
			if ops[i].Kind != '+' {
				oldIdx++
			}
			if ops[i].Kind != '-' {
				newIdx++
			}
			i++
		}
	}
	return hunks
}

func build(ops []edit.Op, a, b [][]byte) Hunk {
	h := Hunk{}
	for _, op := range ops {
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
	return h
}

func start(idx, count int) int {
	if count == 0 {
		return idx
	}
	return idx + 1
}
