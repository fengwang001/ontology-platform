// Package merge3 implements line-based three-way merge.
package merge3

// Change describes replacing base[Start:End] with Lines.
// Start == End means a pure insertion before base line Start.
type Change struct {
	Start int
	End   int
	Lines []string
}

// diff computes the minimal line-level changes that turn base into other
// using a longest-common-subsequence dynamic program. The returned changes
// are sorted by Start and never overlap.
func diff(base, other []string) []Change {
	n, m := len(base), len(other)
	// dp[i][j] = LCS length of base[i:] and other[j:].
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if base[i] == other[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	var changes []Change
	var cur *Change
	flush := func() {
		if cur != nil {
			changes = append(changes, *cur)
			cur = nil
		}
	}
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case base[i] == other[j]:
			flush()
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			// Delete base[i].
			if cur == nil {
				cur = &Change{Start: i, End: i}
			}
			cur.End = i + 1
			i++
		default:
			// Insert other[j] before base[i].
			if cur == nil {
				cur = &Change{Start: i, End: i}
			}
			cur.Lines = append(cur.Lines, other[j])
			j++
		}
	}
	for i < n {
		if cur == nil {
			cur = &Change{Start: i, End: i}
		}
		cur.End = i + 1
		i++
	}
	for j < m {
		if cur == nil {
			cur = &Change{Start: n, End: n}
		}
		cur.Lines = append(cur.Lines, other[j])
		j++
	}
	flush()
	return changes
}
