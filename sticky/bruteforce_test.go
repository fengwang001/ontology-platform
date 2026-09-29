package sticky

// bruteForceMinMigrations 穷举所有满足“每成员持有数差不超过 1”的分配，
// 返回相对 prev 的最小迁移数。prev 中空串表示批前无主。
// 仅用于小规模参照（分区数 <= 8、成员数 <= 4）。
func bruteForceMinMigrations(prev assignment, memberIDs []string) int {
	n := len(prev)
	m := len(memberIDs)
	if m == 0 {
		return 0
	}
	base := n / m

	best := n + 1
	cur := make([]int, n)
	counts := make([]int, m)

	var enumerate func(pos int)
	enumerate = func(pos int) {
		if pos == n {
			for _, c := range counts {
				if c < base || c > base+1 {
					return
				}
			}
			mig := 0
			for p := 0; p < n; p++ {
				if prev[p] != "" && memberIDs[cur[p]] != prev[p] {
					mig++
				}
			}
			if mig < best {
				best = mig
			}
			return
		}
		for idx := 0; idx < m; idx++ {
			if counts[idx] > base+1 {
				continue
			}
			cur[pos] = idx
			counts[idx]++
			enumerate(pos + 1)
			counts[idx]--
		}
	}
	enumerate(0)
	return best
}
