// Package hunk groups an edit script into hunks with C context lines,
// merging changes separated by at most 2C unchanged lines.
package hunk

import "ontology/edit"

// Line is one hunk body line: ' ' context, '-' delete, '+' insert.
type Line struct {
	Kind byte
	Text []byte
}

// Hunk is one @@ -OldStart,OldCount +NewStart,NewCount @@ section.
// A zero Count means Start is the line number before the insertion point.
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Lines              []Line
}

// Group converts ops into hunks with ctx context lines.
// Changes separated by at most 2*ctx unchanged lines merge into one hunk.
func Group(ops []edit.Op, ctx int) []Hunk {
	var hs []Hunk
	for i := 0; i < len(ops); {
		if ops[i].Kind == ' ' {
			i++
			continue
		}
		start := max(i-ctx, 0)
		last := i // index of last change op in this hunk
		for j := i + 1; j < len(ops); j++ {
			if ops[j].Kind != ' ' {
				if j-last-1 > 2*ctx {
					break
				}
				last = j
			}
		}
		end := min(last+1+ctx, len(ops))
		hs = append(hs, build(ops, start, end))
		i = end
	}
	return hs
}

// build creates a Hunk from ops[start:end], computing header numbers.
// A zero count means Start is the line number before the insertion point.
func build(ops []edit.Op, start, end int) Hunk {
	var h Hunk
	for _, op := range ops[:start] {
		if op.Kind != '+' {
			h.OldStart++
		}
		if op.Kind != '-' {
			h.NewStart++
		}
	}
	for _, op := range ops[start:end] {
		h.Lines = append(h.Lines, Line{Kind: op.Kind, Text: op.Text})
		if op.Kind != '+' {
			h.OldCount++
		}
		if op.Kind != '-' {
			h.NewCount++
		}
	}
	if h.OldCount > 0 {
		h.OldStart++
	}
	if h.NewCount > 0 {
		h.NewStart++
	}
	return h
}
