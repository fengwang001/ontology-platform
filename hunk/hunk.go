// Package hunk groups an edit script into unified-diff hunks with C lines of
// context and merges adjacent hunks per GNU diff -U C rules. It depends on
// package edit (and lines through its types).
package hunk

import (
	"ontology/edit"
	"ontology/lines"
)

// Item is one rendered body line of a hunk.
type Item struct {
	Kind edit.Kind
	Text string
	EOL  string
	NoNL bool // true when this line lacks a newline (followed by marker)
}

// Hunk is one contiguous region with its declared old/new coordinates.
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Items              []Item
}

// Build groups ops into hunks using context C. Two edits separated by g
// unchanged lines merge while g <= 2*C (see DESIGN.md section 2).
func Build(script edit.Script, c int) []Hunk {
	ops := script.Ops
	type rng struct{ lo, hi int }
	var edits []rng // maximal runs of non-equal ops
	for i := 0; i < len(ops); {
		if ops[i].Kind == edit.Equal {
			i++
			continue
		}
		j := i
		for j < len(ops) && ops[j].Kind != edit.Equal {
			j++
		}
		edits = append(edits, rng{i, j})
		i = j
	}
	if len(edits) == 0 {
		return nil
	}
	lo := edits[0].lo
	if g := edits[0].lo; g > c {
		lo -= c
	} else {
		lo = 0
	}
	hi := edits[0].hi + c
	if hi > len(ops) {
		hi = len(ops)
	}
	var hs []Hunk
	for _, e := range edits[1:] {
		nextLo, nextHi := e.lo-c, e.hi+c
		if nextLo < 0 {
			nextLo = 0
		}
		if nextHi > len(ops) {
			nextHi = len(ops)
		}
		gap := nextLo - hi
		if gap <= 0 { // contexts overlap or are adjacent: merge
			hi = nextHi
			continue
		}
		hs = append(hs, makeHunk(ops[:lo], ops[lo:hi]))
		lo, hi = nextLo, nextHi
	}
	hs = append(hs, makeHunk(ops[:lo], ops[lo:hi]))
	return hs
}

func makeHunk(before, body []edit.Op) Hunk {
	h := Hunk{Items: make([]Item, 0, len(body))}
	for _, op := range body {
		switch op.Kind {
		case edit.Equal:
			h.OldCount++
			h.NewCount++
			h.Items = append(h.Items, item(edit.Equal, op.A))
		case edit.Delete:
			h.OldCount++
			h.Items = append(h.Items, item(edit.Delete, op.A))
		case edit.Insert:
			h.NewCount++
			h.Items = append(h.Items, item(edit.Insert, op.B))
		}
	}
	h.OldStart = startCoord(before, h.OldCount, true)
	h.NewStart = startCoord(before, h.NewCount, false)
	return h
}

func item(k edit.Kind, l lines.Line) Item {
	return Item{Kind: k, Text: l.Text, EOL: l.EOL, NoNL: l.EOL == ""}
}

func startCoord(before []edit.Op, count int, old bool) int {
	n := 0
	for _, op := range before {
		if old && (op.Kind == edit.Equal || op.Kind == edit.Delete) {
			n++
		}
		if !old && (op.Kind == edit.Equal || op.Kind == edit.Insert) {
			n++
		}
	}
	if count == 0 {
		return n // empty block: line number just before the insertion point
	}
	return n + 1
}
