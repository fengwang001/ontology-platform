// Package merge3 implements line-based three-way merge.
package merge3

// lcsMatches computes a longest-common-subsequence alignment between base and
// side, returning for each base index the matching side index, or -1 when the
// base line has no match. Matched indexes are strictly increasing on both
// sides, so they can be used as alignment anchors.
func lcsMatches(base, side []string) []int {
	n, m := len(base), len(side)
	matches := make([]int, n)
	for i := range matches {
		matches[i] = -1
	}
	if n == 0 || m == 0 {
		return matches
	}

	// dp[i][j] = LCS length of base[i:] and side[j:].
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			switch {
			case base[i] == side[j]:
				dp[i][j] = dp[i+1][j+1] + 1
			case dp[i+1][j] >= dp[i][j+1]:
				dp[i][j] = dp[i+1][j]
			default:
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	for i, j := 0, 0; i < n && j < m; {
		switch {
		case base[i] == side[j]:
			matches[i] = j
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			i++
		default:
			j++
		}
	}
	return matches
}
