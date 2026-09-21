// Package quorum computes majority thresholds and commit points for a
// fixed set of n replicas. It does not depend on package replica.
package quorum

import "sort"

// Majority returns the number of replicas required for a majority of n.
// For n = 1..7 it yields 1, 2, 2, 3, 3, 4, 4. For n < 1 it returns 0.
func Majority(n int) int {
	if n < 1 {
		return 0
	}
	return n/2 + 1
}

// CommitIndex returns the highest index that at least Majority(n) of the
// given match values have reached. matches holds one "highest matched
// index" per replica; replicas that are unreachable simply contribute a
// lower (or zero) value. A minority that runs ahead can never raise the
// result past what a majority has matched.
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
