// Package ord defines the total order on elements (score desc, id asc)
// and pure batch functions that compute the three rankings of a sequence
// already arranged in that order. It has no dependencies on other packages.
package ord

import "sort"

// Element is one streamed item: a unique ID and a score.
type Element struct {
	ID    string
	Score int64
}

// Triple is the materialized ranking of one element.
type Triple struct {
	RowNumber int
	Rank      int
	DenseRank int
}

// Less reports whether a precedes b in the total order: higher score first;
// equal scores are broken by the smaller ID (IDs are unique).
func Less(a, b Element) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	return a.ID < b.ID
}

// Sort arranges elems in place by score desc and then id asc.
func Sort(elems []Element) {
	sort.SliceStable(elems, func(i, j int) bool { return Less(elems[i], elems[j]) })
}

// Batch computes ROW_NUMBER / RANK / DENSE_RANK for a sequence that is
// already sorted by Less. RANK of a group is 1 + the number of strictly
// higher elements, i.e. the 1-based position of the group's first member;
// DENSE_RANK is 1 + the number of strictly higher distinct scores.
func Batch(sorted []Element) map[string]Triple {
	out := make(map[string]Triple, len(sorted))
	rank, dense := 0, 0
	first := true
	var prev int64
	for i, e := range sorted {
		if first || e.Score != prev {
			dense++
			rank = i + 1 // first position of this score group
			first = false
		}
		out[e.ID] = Triple{RowNumber: i + 1, Rank: rank, DenseRank: dense}
		prev = e.Score
	}
	return out
}
