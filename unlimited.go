package ontology

// DistanceUnlimited computes the exact Levenshtein distance between a and
// b in Unicode code points, with no threshold and no early termination.
// It uses a plain full-width rolling-array DP (O(min(len)) memory) and
// exists mainly as a reference to cross-check Distance: whenever Distance
// reports a distance (Exceeded == false), it must equal DistanceUnlimited.
func DistanceUnlimited(a, b string) (int, error) {
	ra, err := decodeRunes(a, SideLeft)
	if err != nil {
		return 0, err
	}
	rb, err := decodeRunes(b, SideRight)
	if err != nil {
		return 0, err
	}
	m, n := len(ra), len(rb)
	if n > m {
		ra, rb = rb, ra
		m, n = n, m
	}
	prev := make([]int, n+1)
	cur := make([]int, n+1)
	for j := 0; j <= n; j++ {
		prev[j] = j
	}
	for i := 1; i <= m; i++ {
		cur[0] = i
		for j := 1; j <= n; j++ {
			cost := 0
			if ra[i-1] != rb[j-1] {
				cost = 1
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[n], nil
}
