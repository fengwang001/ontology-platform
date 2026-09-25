package ontology

// RankWithCount behaves exactly like Rank but also returns the number of
// comparator invocations used to order rows within partitions. It lets
// callers assert the comparison budget is O(n log n) rather than O(n^2).
func RankWithCount(rows []Row, dir Direction) (RankResult, int) {
	total := 0
	res := rankWithLess(rows, dir, func(counter *int) lessFunc {
		base := baseLess(dir)
		return func(a, b sortedRow) bool {
			total++
			*counter++
			return base(a, b)
		}
	})
	return res, total
}
