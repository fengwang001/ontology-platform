// Package merge3 implements a line-level three-way merge.
package merge3

// hunk describes one contiguous edit that turns a slice of base
// (base[start:end]) into the given replacement lines.
type hunk struct {
	start, end int
	lines      []string
}

// diff computes the line-level hunks that transform base into side,
// using a longest-common-subsequence dynamic program. Hunks are
// returned in ascending base order and never overlap.
func diff(base, side []string) []hunk {
	pre := 0
	for pre < len(base) && pre < len(side) && base[pre] == side[pre] {
		pre++
	}
	suf := 0
	for suf < len(base)-pre && suf < len(side)-pre &&
		base[len(base)-1-suf] == side[len(side)-1-suf] {
		suf++
	}
	bMid := base[pre : len(base)-suf]
	sMid := side[pre : len(side)-suf]
	n, m := len(bMid), len(sMid)
	if n == 0 && m == 0 {
		return nil
	}

	// dp[i][j] = LCS length of bMid[i:] and sMid[j:], stored flat.
	dp := make([]int, (n+1)*(m+1))
	at := func(i, j int) *int { return &dp[i*(m+1)+j] }
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if bMid[i] == sMid[j] {
				*at(i, j) = *at(i+1, j+1) + 1
			} else if *at(i+1, j) >= *at(i, j+1) {
				*at(i, j) = *at(i+1, j)
			} else {
				*at(i, j) = *at(i, j+1)
			}
		}
	}

	var hunks []hunk
	var cur *hunk
	closeCur := func() {
		if cur != nil {
			hunks = append(hunks, *cur)
			cur = nil
		}
	}
	open := func(i int) {
		if cur == nil {
			cur = &hunk{start: pre + i, end: pre + i}
		}
	}
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && bMid[i] == sMid[j]:
			closeCur()
			i++
			j++
		case j < m && (i == n || *at(i, j+1) >= *at(i+1, j)):
			open(i)
			cur.lines = append(cur.lines, sMid[j])
			j++
		default:
			open(i)
			i++
			cur.end = pre + i
		}
	}
	closeCur()
	return hunks
}
