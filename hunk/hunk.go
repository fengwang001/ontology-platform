// Package hunk groups an edit script into hunks with C lines of context.
// Two changes separated by at most 2*C unchanged lines merge into one
// hunk (the GNU diff -U C rule).
package hunk

import "ontology/edit"

// Hunk is one @@ block: a run of ops plus its line ranges in both files.
// A start of 0 with count 0 means "before line 1" (see DESIGN.md).
type Hunk struct {
	OldStart, OldCount, NewStart, NewCount int
	Ops                                    []edit.Op
}

// Build groups ops into hunks with ctx lines of leading/trailing context.
func Build(ops []edit.Op, ctx int) []Hunk {
	var hs []Hunk
	oldLn, newLn, i := 1, 1, 0
	for i < len(ops) {
		j := i
		for j < len(ops) && ops[j].Kind == ' ' {
			j++
		}
		if j == len(ops) {
			break
		}
		oldLn += j - i
		newLn += j - i
		end := j
		for {
			p := end
			for p < len(ops) && ops[p].Kind != ' ' {
				p++
			}
			q := p
			for q < len(ops) && ops[q].Kind == ' ' {
				q++
			}
			end = p
			if q == len(ops) || q-p > 2*ctx {
				break
			}
			end = q
		}
		lo := max(j-ctx, i)
		hi := min(end+ctx, len(ops))
		h := Hunk{Ops: ops[lo:hi], OldStart: oldLn - (j - lo), NewStart: newLn - (j - lo)}
		for _, op := range h.Ops {
			if op.Kind != '+' {
				h.OldCount++
			}
			if op.Kind != '-' {
				h.NewCount++
			}
		}
		if h.OldCount == 0 {
			h.OldStart--
		}
		if h.NewCount == 0 {
			h.NewStart--
		}
		for _, op := range ops[j:hi] {
			switch op.Kind {
			case ' ':
				oldLn++
				newLn++
			case '-':
				oldLn++
			case '+':
				newLn++
			}
		}
		hs = append(hs, h)
		i = hi
	}
	return hs
}
