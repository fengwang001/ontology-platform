// Package merge3 implements line-level three-way merge.
package merge3

// diffLines computes a line-level alignment from base to side using a
// longest-common-subsequence dynamic program.
//
// keep[i] reports whether base[i] survives in side. ins[i] holds the lines
// of side inserted immediately before base[i]; ins[len(base)] holds lines
// appended after the last base line. Every side line is accounted for
// exactly once, so applying (keep, ins) to base reproduces side.
func diffLines(base, side []string) (keep []bool, ins [][]string) {
	n, m := len(base), len(side)
	// dp[i][j] is the LCS length of base[i:] and side[j:].
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

	keep = make([]bool, n)
	ins = make([][]string, n+1)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case base[i] == side[j]:
			keep[i] = true
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			// Delete base[i]; on ties this anchors insertions as
			// late as possible.
			i++
		default:
			ins[i] = append(ins[i], side[j])
			j++
		}
	}
	for ; j < m; j++ {
		ins[n] = append(ins[n], side[j])
	}
	return keep, ins
}
