// Package hunk groups an edit script into hunks with c context lines.
// Two changes merge into one hunk when the gap of unchanged lines
// between them is at most 2*c (see DESIGN.md section 2).
package hunk

import "ontology/edit"

// Hunk is a contiguous group of ops. OldStart and NewStart are 0-based
// positions in the old and new sequences (for zero-count sides they are
// the insertion point, i.e. the number of preceding lines).
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Ops                []edit.Op
}

// Group splits ops into hunks with c lines of context around changes.
func Group(ops []edit.Op, c int) []Hunk {
	var hs []Hunk
	for i := 0; i < len(ops); {
		if ops[i].Kind == ' ' {
			i++
			continue
		}
		s := max(i-c, 0)
		j := i
		for {
			for j < len(ops) && ops[j].Kind != ' ' {
				j++
			}
			k := j
			for k < len(ops) && ops[k].Kind == ' ' {
				k++
			}
			if k == len(ops) { // trailing equal run: keep up to c
				j = min(j+c, k)
				break
			}
			if k-j > 2*c { // gap too wide: close hunk after c context
				j += c
				break
			}
			j = k // merge: gap fits within both sides' context
		}
		hs = append(hs, makeHunk(ops[s:j]))
		i = j
	}
	return hs
}

func makeHunk(ops []edit.Op) Hunk {
	h := Hunk{Ops: ops}
	if len(ops) == 0 {
		return h
	}
	h.OldStart, h.NewStart = ops[0].Old, ops[0].New
	for _, o := range ops {
		if o.Kind != '+' {
			h.OldCount++
		}
		if o.Kind != '-' {
			h.NewCount++
		}
	}
	return h
}
