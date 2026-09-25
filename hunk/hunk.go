// Package hunk groups an edit script into context-limited unified hunks.
// Two changes are merged when the unchanged gap between them is <= 2*ctx.
package hunk

import "ontology/edit"

// Line is one rendered hunk row. Kind is ' ', '-' or '+'; Text is the raw
// file line, including its original terminator.
type Line struct {
	Kind byte
	Text []byte
}

// Hunk is one unified hunk. Starts/counts follow GNU conventions: a zero
// count has a start equal to the number of preceding lines.
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Lines              []Line
}

// Build groups the script (between a and b) into hunks with ctx context lines.
func Build(s edit.Script, a, b [][]byte, ctx int) []Hunk {
	var changes []int
	for i, op := range s {
		if op.Kind != ' ' {
			changes = append(changes, i)
		}
	}
	if len(changes) == 0 {
		return nil
	}
	var out []Hunk
	for gi := 0; gi < len(changes); {
		first, last := changes[gi], changes[gi]
		for gi++; gi < len(changes); gi++ {
			if changes[gi]-last > 2*ctx+1 { // ops strictly between > 2*ctx
				break
			}
			last = changes[gi]
		}
		lo, hi := first, last
		for c := 0; c < ctx && lo > 0 && s[lo-1].Kind == ' '; c++ {
			lo--
		}
		for c := 0; c < ctx && hi < len(s)-1 && s[hi+1].Kind == ' '; c++ {
			hi++
		}
		out = append(out, buildOne(s, a, b, lo, hi))
	}
	return out
}

func buildOne(s edit.Script, a, b [][]byte, lo, hi int) Hunk {
	h := Hunk{}
	var firstOld, firstNew int = -1, -1
	var insertBeforeOld, insertBeforeNew int
	for i := lo; i <= hi; i++ {
		op := s[i]
		switch op.Kind {
		case ' ':
			h.Lines = append(h.Lines, Line{' ', a[op.Old]})
			if firstOld < 0 {
				firstOld = op.Old
			}
			if firstNew < 0 {
				firstNew = op.New
			}
			h.OldCount++
			h.NewCount++
		case '-':
			h.Lines = append(h.Lines, Line{'-', a[op.Old]})
			if firstOld < 0 {
				firstOld = op.Old
			}
			insertBeforeNew = op.New
			h.OldCount++
		case '+':
			h.Lines = append(h.Lines, Line{'+', b[op.New]})
			if firstNew < 0 {
				firstNew = op.New
			}
			insertBeforeOld = op.Old
			h.NewCount++
		}
	}
	if h.OldCount > 0 {
		h.OldStart = firstOld + 1
	} else {
		h.OldStart = insertBeforeOld
	}
	if h.NewCount > 0 {
		h.NewStart = firstNew + 1
	} else {
		h.NewStart = insertBeforeNew
	}
	return h
}

// Make is a convenience: diff a/b and group the result in one call.
func Make(a, b [][]byte, ctx, maxDist int) ([]Hunk, error) {
	s, err := edit.Diff(a, b, maxDist)
	if err != nil {
		return nil, err
	}
	return Build(s, a, b, ctx), nil
}
