package topk

import "sort"

// better reports whether (aScore, aID) ranks strictly ahead of
// (bScore, bID). The direction affects only the score dimension:
// Desc prefers larger scores, Asc prefers smaller ones. Tied scores
// are always broken by ID in ascending lexicographic order, and Go's
// == treats +0.0 and -0.0 as equal. NaN never reaches here because
// Push rejects it before it enters the selector.
func better(dir Direction, aScore float64, aID string, bScore float64, bID string) bool {
	if aScore != bScore {
		if dir == Desc {
			return aScore > bScore
		}
		return aScore < bScore
	}
	return aID < bID
}

// worse reports whether a ranks behind b in the same order.
func worse(dir Direction, aScore float64, aID string, bScore float64, bID string) bool {
	return better(dir, bScore, bID, aScore, aID)
}

// bestFirst sorts elements into snapshot (best-first) order.
func bestFirst(dir Direction, elems []Element) {
	sort.Slice(elems, func(i, j int) bool {
		return better(dir, elems[i].Score, elems[i].ID, elems[j].Score, elems[j].ID)
	})
}
