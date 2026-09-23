// Package phrase implements phrase queries (positional alignment) and
// boolean AND queries over posting lists, with comparison counters.
package phrase

import (
	"sync/atomic"

	"ontology/posting"
)

// Hit is one document match: the start positions of the phrase in it.
type Hit struct {
	Doc    uint32
	Starts []uint32
}

var (
	posCmp atomic.Int64
	andCmp atomic.Int64
)

// ResetCounters zeroes both comparison counters.
func ResetCounters() {
	posCmp.Store(0)
	andCmp.Store(0)
}

// PosComparisons returns position/doc comparisons done by Phrase so far.
func PosComparisons() int64 { return posCmp.Load() }

// AndComparisons returns doc comparisons done by And so far.
func AndComparisons() int64 { return andCmp.Load() }

// Phrase returns the hits of the phrase t1..tk given each term's posting
// list. Lists must be sorted by doc, positions sorted inside each doc.
// A single-term phrase degenerates to a term query. Overlapping matches
// are counted (see DESIGN.md).
func Phrase(lists []posting.List) []Hit {
	if len(lists) == 0 {
		return nil
	}
	if len(lists) == 1 {
		hits := make([]Hit, 0, len(lists[0]))
		for _, e := range lists[0] {
			hits = append(hits, Hit{Doc: e.Doc, Starts: e.Pos})
		}
		return hits
	}
	var hits []Hit
	ptr := make([]int, len(lists))
outer:
	for _, e0 := range lists[0] {
		entries := make([]posting.Entry, len(lists))
		entries[0] = e0
		for j := 1; j < len(lists); j++ {
			l := lists[j]
			for ptr[j] < len(l) && l[ptr[j]].Doc < e0.Doc {
				posCmp.Add(1)
				ptr[j]++
			}
			if ptr[j] >= len(l) {
				break outer
			}
			posCmp.Add(1)
			if l[ptr[j]].Doc != e0.Doc {
				continue outer
			}
			entries[j] = l[ptr[j]]
		}
		hits = appendStarts(hits, e0.Doc, align(entries))
	}
	return hits
}

func appendStarts(hits []Hit, doc uint32, starts []uint32) []Hit {
	if len(starts) == 0 {
		return hits
	}
	return append(hits, Hit{Doc: doc, Starts: starts})
}

// align merges the k position lists of one document on adjusted values
// v_j = pos - (j-1): a hit needs all v_j equal. On mismatch every pointer
// below the max advances; on a hit every pointer advances by one, which
// allows overlapping matches. Each round advances >=1 pointer, so the
// number of rounds is bounded by the sum of list lengths.
func align(entries []posting.Entry) []uint32 {
	k := len(entries)
	ptr := make([]int, k)
	var starts []uint32
	for {
		for j := 0; j < k; j++ {
			if ptr[j] >= len(entries[j].Pos) {
				return starts
			}
		}
		vmax := int64(-1)
		for j := 0; j < k; j++ {
			v := int64(entries[j].Pos[ptr[j]]) - int64(j)
			posCmp.Add(1)
			if v > vmax {
				vmax = v
			}
		}
		allEqual := true
		for j := 0; j < k; j++ {
			v := int64(entries[j].Pos[ptr[j]]) - int64(j)
			posCmp.Add(1)
			if v != vmax {
				allEqual = false
			}
		}
		if allEqual {
			starts = append(starts, uint32(vmax))
			for j := 0; j < k; j++ {
				ptr[j]++
			}
			continue
		}
		for j := 0; j < k; j++ {
			v := int64(entries[j].Pos[ptr[j]]) - int64(j)
			if v < vmax {
				ptr[j]++
			}
		}
	}
}

// And intersects posting lists at the document level. The shortest list
// drives; every other list keeps a persistent pointer that only moves
// forward, so comparisons stay proportional to the list lengths.
func And(lists []posting.List) []uint32 {
	if len(lists) == 0 {
		return nil
	}
	driver := 0
	for i := range lists {
		if len(lists[i]) < len(lists[driver]) {
			driver = i
		}
	}
	if len(lists[driver]) == 0 {
		return nil
	}
	ptr := make([]int, len(lists))
	var out []uint32
outer:
	for _, e := range lists[driver] {
		for j := range lists {
			if j == driver {
				continue
			}
			l := lists[j]
			for ptr[j] < len(l) && l[ptr[j]].Doc < e.Doc {
				andCmp.Add(1)
				ptr[j]++
			}
			if ptr[j] >= len(l) {
				break outer
			}
			andCmp.Add(1)
			if l[ptr[j]].Doc != e.Doc {
				continue outer
			}
		}
		out = append(out, e.Doc)
	}
	return out
}
