// Package hunk groups an edit script into hunks with C context lines.
// Two change runs merge into one hunk when the gap of unchanged lines
// between them is <= 2*C (see DESIGN.md section 2).
package hunk

import "ontology/edit"

// Hunk is a contiguous slice of an edit script containing at least one
// change, padded with up to C context ops on each side. AStart/BStart are
// 0-based line indexes of the first op in the old/new sequences.
type Hunk struct {
	AStart, BStart int
	Ops            []edit.Op
}

// ACount returns the number of old-sequence lines (Keep+Del) in the hunk.
func (h Hunk) ACount() int { return h.count(true) }

// BCount returns the number of new-sequence lines (Keep+Ins) in the hunk.
func (h Hunk) BCount() int { return h.count(false) }

func (h Hunk) count(old bool) int {
	n := 0
	for _, op := range h.Ops {
		if op.Kind == edit.Keep || (old && op.Kind == edit.Del) || (!old && op.Kind == edit.Ins) {
			n++
		}
	}
	return n
}

// advance moves the 0-based old/new positions past one op.
func advance(op edit.Op, a, b int) (int, int) {
	switch op.Kind {
	case edit.Keep:
		return a + 1, b + 1
	case edit.Del:
		return a + 1, b
	}
	return a, b + 1
}

// Group splits ops into hunks with c lines of context. Change runs
// separated by at most 2*c Keep ops merge into a single hunk.
func Group(ops []edit.Op, c int) []Hunk {
	var hunks []Hunk
	n := len(ops)
	aPos, bPos := 0, 0 // 0-based positions at ops[i]
	for i := 0; i < n; {
		for i < n && ops[i].Kind == edit.Keep {
			aPos, bPos = advance(ops[i], aPos, bPos)
			i++
		}
		if i == n {
			break
		}
		start, sA, sB := i, aPos, bPos
		for back := 0; start > 0 && ops[start-1].Kind == edit.Keep && back < c; back++ {
			start--
			sA--
			sB--
		}
		lastChange := i
		for j := i; j < n; {
			if ops[j].Kind != edit.Keep {
				lastChange = j
				j++
				continue
			}
			k := j
			for k < n && ops[k].Kind == edit.Keep {
				k++
			}
			if k == n || k-j > 2*c {
				break
			}
			j = k
		}
		end := lastChange + 1
		for t := 0; end < n && ops[end].Kind == edit.Keep && t < c; t++ {
			end++
		}
		hunks = append(hunks, Hunk{AStart: sA, BStart: sB, Ops: ops[start:end]})
		aPos, bPos = sA, sB
		for _, op := range ops[start:end] {
			aPos, bPos = advance(op, aPos, bPos)
		}
		i = end
	}
	return hunks
}
