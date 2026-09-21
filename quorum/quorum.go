// Package quorum computes majority thresholds and commit points.
package quorum

import "sort"

// Majority returns the majority threshold for n replicas:
// the smallest count strictly greater than n/2.
func Majority(n int) int {
	if n < 1 {
		return 0
	}
	return (n + 1) / 2
}

// CommitIndex returns the highest index that at least a majority of the
// n replicas have matched. A fast minority can never raise the commit
// point. It returns 0 when fewer than a majority report a match.
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
