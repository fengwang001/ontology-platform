// Package hunk groups an edit script into hunks with ctx context
// lines. Two change blocks merge into one hunk when the gap of
// unchanged lines between them is <= 2*ctx (see DESIGN.md §2).
package hunk

import "ontology/edit"

// Hunk is a slice of an edit script. Old0 and New0 are 0-based
// indices of the hunk's first line in the old and new sequences
// (for a pure insertion, the insertion point).
type Hunk struct {
	Old0, New0 int
	Ops        []edit.Op
}

// Group splits ops into hunks with ctx lines of trailing/leading
// context, merging changes separated by at most 2*ctx kept lines.
func Group(ops []edit.Op, ctx int) []Hunk {
	var hs []Hunk
	i, o, n, sp := 0, 0, 0, 0
	for i < len(ops) {
		if ops[i].Kind == ' ' {
			i, o, n, sp = i+1, o+1, n+1, sp+1
			continue
		}
		lead := min(ctx, sp)
		s := i - lead
		last := i
		j := i
		for j < len(ops) {
			if ops[j].Kind != ' ' {
				last = j
				j++
				continue
			}
			k := j
			for k < len(ops) && ops[k].Kind == ' ' {
				k++
			}
			if k == len(ops) || k-j > 2*ctx {
				break
			}
			j = k
		}
		e := last + 1
		for t := 0; t < ctx && e < len(ops) && ops[e].Kind == ' '; t++ {
			e++
		}
		hs = append(hs, Hunk{Old0: o - lead, New0: n - lead, Ops: ops[s:e]})
		for ; i < e; i++ {
			if ops[i].Kind != '+' {
				o++
			}
			if ops[i].Kind != '-' {
				n++
			}
		}
		sp = 0
	}
	return hs
}
