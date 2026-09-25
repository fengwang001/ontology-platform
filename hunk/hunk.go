package hunk

import (
	"ontology/edit"
)

// Hunk is one contiguous region of a unified diff.
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Ops                []edit.Op // Equal/Delete/Insert, in file order
}

// Group partitions a script into hunks with C lines of context,
// merging adjacent change groups separated by at most 2*C common lines
// (see DESIGN.md section 2).
func Group(s edit.Script, c int) []Hunk {
	if len(s) == 0 {
		return nil
	}
	// Change-group spans as indices into s.
	var spans [][2]int
	i := 0
	for i < len(s) {
		if s[i].Kind == edit.Equal {
			i++
			continue
		}
		j := i
		for j < len(s) && s[j].Kind != edit.Equal {
			j++
		}
		spans = append(spans, [2]int{i, j})
		i = j
	}
	if len(spans) == 0 {
		return nil
	}
	// Merge neighboring spans whose common-run length is <= 2*c.
	merged := [][2]int{spans[0]}
	for _, sp := range spans[1:] {
		last := merged[len(merged)-1]
		gap := sp[0] - last[1] // consecutive Equal ops
		if gap <= 2*c {
			merged[len(merged)-1][1] = sp[1]
		} else {
			merged = append(merged, sp)
		}
	}
	// Expand each merged span by c equal ops on each side, disjoint.
	var hunks []Hunk
	for q, sp := range merged {
		lo, hi := sp[0], sp[1]
		add := c
		for lo > 0 && s[lo-1].Kind == edit.Equal && add > 0 {
			lo--
			add--
		}
		add = c
		for hi < len(s) && s[hi].Kind == edit.Equal && add > 0 {
			hi++
			add--
		}
		if q+1 < len(merged) {
			// Guarantee no overlap with next expanded span.
			if nxt := merged[q+1][0]; hi > nxt-c && nxt-c >= 0 {
				hi = nxt - c
				if hi < sp[1] {
					hi = sp[1]
				}
			}
		}
		hunks = append(hunks, build(s[lo:hi]))
	}
	return hunks
}

func build(ops []edit.Op) Hunk {
	h := Hunk{Ops: ops}
	for _, op := range ops {
		switch op.Kind {
		case edit.Equal:
			h.OldCount++
			h.NewCount++
		case edit.Delete:
			h.OldCount++
		case edit.Insert:
			h.NewCount++
		}
	}
	h.OldStart, h.NewStart = startOf(ops)
	return h
}

// startOf returns the 1-based starts following GNU unified-diff rules,
// including the zero-count convention (DESIGN.md section 1).
func startOf(ops []edit.Op) (oldStart, newStart int) {
	oldSeen, newSeen := 0, 0
	oldSet, newSet := false, false
	for _, op := range ops {
		switch op.Kind {
		case edit.Equal:
			if !oldSet || !newSet {
				if !oldSet {
					oldStart = oldSeen + 1
				}
				if !newSet {
					newStart = newSeen + 1
				}
				oldSet, newSet = true, true
			}
			oldSeen++
			newSeen++
		case edit.Delete:
			if !oldSet {
				oldStart = oldSeen + 1
				oldSet = true
			}
			oldSeen++
			if !newSet {
				newStart = newSeen // line after insertion point
			}
		case edit.Insert:
			if !newSet {
				newStart = newSeen + 1
				newSet = true
			}
			newSeen++
			if !oldSet {
				oldStart = oldSeen // zero-count: preceding old line
			}
		}
	}
	return oldStart, newStart
}
