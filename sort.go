package ontology

import "sort"

// rankedSort orders elements best-first: Score by direction, then ID
// ascending for ties.
func rankedSort(dir Direction, es []Element) {
	sort.Slice(es, func(i, j int) bool {
		return better(dir, es[i], es[j])
	})
}

// sameScore reports whether two scores are the same rank value, treating
// +0.0 and -0.0 as equal.
func sameScore(a, b float64) bool {
	return normZero(a) == normZero(b)
}
