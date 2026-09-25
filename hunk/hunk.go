// Package hunk groups an edit script into unified-diff hunks with a
// configurable number C of surrounding context lines.
package hunk

import (
	"ontology/edit"
	"ontology/lines"
)

// Item is one rendered line inside a hunk.
type Item struct {
	Kind edit.Kind // Equal, Delete, Insert
	Line lines.Line
}

// Hunk is one contiguous group of items.
type Hunk struct {
	Items            []Item
	OldStart, OldN   int
	NewStart, NewN   int
	OldNoNL, NewNoNL bool
}

// Group partitions a script into hunks. Two change blocks separated by g
// unchanged lines merge when g <= 2C and split otherwise.
func Group(s *edit.Script, context int) []Hunk {
	if context < 0 {
		context = 0
	}
	ops := s.Ops
	var changes [][]int // index ranges [start,end) covering each change block
	start := -1
	for i, op := range ops {
		if op.Kind == edit.Equal {
			if start >= 0 {
				changes = append(changes, []int{start, i})
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		changes = append(changes, []int{start, len(ops)})
	}
	if len(changes) == 0 {
		return nil
	}
	type span struct{ lo, hi int }
	var spans []span
	cur := span{lo: changes[0][0], hi: changes[0][1]}
	for _, ch := range changes[1:] {
		gap := ch[0] - cur.hi // unchanged Equal ops between blocks
		if gap <= 2*context {
			cur.hi = ch[1]
			continue
		}
		spans = append(spans, cur)
		cur = span{lo: ch[0], hi: ch[1]}
	}
	spans = append(spans, cur)

	var hunks []Hunk
	for _, sp := range spans {
		prefixOld, prefixNew := 0, 0
		for _, op := range ops[:sp.lo] {
			if op.Kind != edit.Insert {
				prefixOld++
			}
			if op.Kind != edit.Delete {
				prefixNew++
			}
		}
		lo := sp.lo - context
		if lo < 0 {
			lo = 0
		}
		hi := sp.hi + context
		if hi > len(ops) {
			hi = len(ops)
		}
		h := Hunk{}
		n, m, first := 0, 0, -1
		for _, op := range ops[lo:hi] {
			idx := len(h.Items)
			switch op.Kind {
			case edit.Equal:
				n++
				m++
			case edit.Delete:
				n++
			case edit.Insert:
				m++
			}
			if op.Kind != edit.Equal && first < 0 {
				first = idx
			}
			h.Items = append(h.Items, Item{Kind: op.Kind, Line: op.Line})
		}
		leadEq := 0
		for i := 0; i < first; i++ {
			if h.Items[i].Kind == edit.Equal {
				leadEq++
			}
		}
		h.OldStart, h.NewStart = prefixOld+leadEq, prefixNew+leadEq
		if n == 0 {
			h.OldStart = prefixOld
		}
		if m == 0 {
			h.NewStart = prefixNew
		}
		h.OldN, h.NewN = n, m
		hunks = append(hunks, h)
	}
	return hunks
}
