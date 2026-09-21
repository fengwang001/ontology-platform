// Package quorum computes majority thresholds and commit points.
package quorum

import "sort"

// Majority returns the majority threshold for n replicas.
func Majority(n int) int {
	if n < 1 {
		return 0
	}
	return n/2 + 1
}

// CommitIndex returns the highest Index matched by a majority of n replicas.
// A leading minority can never move the commit point by itself.
func CommitIndex(matches []uint64, n int) uint64 {
	need := Majority(n)
	if need == 0 || len(matches) < need {
		return 0
	}
	sorted := make([]uint64, len(matches))
	copy(sorted, matches)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] > sorted[j] })
	return sorted[need-1]
}
