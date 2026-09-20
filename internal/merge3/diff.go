package merge3

// hunk describes replacing base[start:end] with lines.
type hunk struct {
	start int
	end   int
	lines []string
}

func (h hunk) empty() bool { return h.start == h.end }

// diff computes the hunks that turn base into other, using an LCS match.
func diff(base, other []string) []hunk {
	matches := lcsMatches(base, other)
	var hunks []hunk
	b, o := 0, 0
	flush := func(bEnd, oEnd int) {
		if b == bEnd && o == oEnd {
			return
		}
		lines := make([]string, oEnd-o)
		copy(lines, other[o:oEnd])
		hunks = append(hunks, hunk{start: b, end: bEnd, lines: lines})
	}
	for _, m := range matches {
		flush(m[0], m[1])
		b, o = m[0]+1, m[1]+1
	}
	flush(len(base), len(other))
	return hunks
}

// lcsMatches returns index pairs (a[i], b[j]) of a longest common
// subsequence between a and b, in increasing order.
func lcsMatches(a, b []string) [][2]int {
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			switch {
			case a[i] == b[j]:
				dp[i][j] = dp[i+1][j+1] + 1
			case dp[i+1][j] >= dp[i][j+1]:
				dp[i][j] = dp[i+1][j]
			default:
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var matches [][2]int
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			matches = append(matches, [2]int{i, j})
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
