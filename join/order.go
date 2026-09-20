package join

import "sort"

// sortPairs 把匹配行对排成确定顺序：先按连接键逐列升序，
// 同键内按 (左行标识, 右行标识) 升序。行标识由内容派生，
// 因此顺序与两表的输入顺序无关。
func sortPairs(pairs []pair, leftIDs, rightIDs []string) {
	sort.SliceStable(pairs, func(a, b int) bool {
		pa, pb := pairs[a], pairs[b]
		for col := range pa.vals {
			if c := compareCanon(pa.vals[col].val, pb.vals[col].val); c != 0 {
				return c < 0
			}
		}
		if leftIDs[pa.left] != leftIDs[pb.left] {
			return leftIDs[pa.left] < leftIDs[pb.left]
		}
		return rightIDs[pa.right] < rightIDs[pb.right]
	})
}

// sortUnmatched 把 Left 模式未匹配的左行按行标识升序排列，
// 置于全部已匹配行之后。
func sortUnmatched(unmatched []int, leftIDs []string) {
	sort.SliceStable(unmatched, func(a, b int) bool {
		return leftIDs[unmatched[a]] < leftIDs[unmatched[b]]
	})
}
