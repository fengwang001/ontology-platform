// Package hunk groups an edit script into unified-diff hunks with C lines of
// context, merging neighboring hunks per the GNU rule (gap <= 2*C).
package hunk

import "ontology/edit"

// Item is one rendered line of a hunk: ' ', '-', or '+'.
type Item struct {
	Tag  byte
	Text []byte // line bytes without terminator
	EOL  []byte // original terminator: "\n", "\r\n", or ""
}

// Hunk is a contiguous group of edit items with old/new side ranges.
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Items              []Item
}

// Build groups ops into hunks using ctx lines of context.
func Build(ops []edit.Op, ctx int) []Hunk {
	if ctx < 0 {
		ctx = 0
	}
	groups := rawGroups(ops)
	if len(groups) == 0 {
		return nil
	}
	spans := make([][2]int, len(groups)) // [start,end) change indices in ops
	for i, g := range groups {
		spans[i] = g
	}
	merged := [][2]int{spans[0]}
	for i := 1; i < len(spans); i++ {
		prev := merged[len(merged)-1]
		gap := gapKeeps(ops, prev[1], spans[i][0])
		if gap <= 2*ctx {
			merged[len(merged)-1][1] = spans[i][1]
		} else {
			merged = append(merged, spans[i])
		}
	}
	out := make([]Hunk, 0, len(merged))
	for _, sp := range merged {
		lo, hi := expand(ops, sp, ctx)
		out = append(out, makeHunk(ops, lo, hi))
	}
	return out
}

// rawGroups finds contiguous spans (in op indices) containing changes.
func rawGroups(ops []edit.Op) [][2]int {
	var gs [][2]int
	start := -1
	for i, o := range ops {
		changed := o.Kind != edit.Keep
		if changed && start < 0 {
			start = i
		}
		if !changed && start >= 0 {
			gs = append(gs, [2]int{start, i})
			start = -1
		}
	}
	if start >= 0 {
		gs = append(gs, [2]int{start, len(ops)})
	}
	return gs
}

func gapKeeps(ops []edit.Op, from, to int) int {
	n := 0
	for i := from; i < to; i++ {
		if ops[i].Kind == edit.Keep {
			n++
		}
	}
	return n
}

func expand(ops []edit.Op, sp [2]int, ctx int) (int, int) {
	lo, hi := sp[0], sp[1]
	for c := 0; lo > 0 && c < ctx; c++ {
		lo--
	}
	for c := 0; hi < len(ops) && c < ctx; c++ {
		hi++
	}
	return lo, hi
}

func makeHunk(ops []edit.Op, lo, hi int) Hunk {
	h := Hunk{}
	oldBefore, newBefore := 0, 0
	for _, o := range ops[:lo] {
		if o.Kind != edit.Insert {
			oldBefore++
		}
		if o.Kind != edit.Delete {
			newBefore++
		}
	}
	h.OldStart = oldBefore
	h.NewStart = newBefore
	for _, o := range ops[lo:hi] {
		var tag byte
		switch o.Kind {
		case edit.Keep:
			tag = ' '
			h.OldCount++
			h.NewCount++
		case edit.Delete:
			tag = '-'
			h.OldCount++
		case edit.Insert:
			tag = '+'
			h.NewCount++
		}
		h.Items = append(h.Items, Item{Tag: tag, Text: o.Line.Text, EOL: o.Line.EOL})
	}
	return h
}
