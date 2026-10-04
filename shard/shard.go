// Package shard 按估计时长把用例分配到常规分片。
package shard

import "sort"

// LowerMedian 返回 vals 升序排列后第 ⌈m/2⌉ 个元素（m 为元素个数，从 1 计）。
// 空切片返回 1。
func LowerMedian(vals []int64) int64 {
	if len(vals) == 0 {
		return 1
	}
	sorted := append([]int64(nil), vals...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[(len(sorted)+1)/2-1]
}

// Assign 把 names 按 (est 降序, 名字字节序升序) 依次放入当前 est 总和
// 最小的分片，并列取下标小者；分片内按放入次序列出。返回 n 个分片。
func Assign(names []string, est func(string) int64, n int) [][]string {
	ests := make(map[string]int64, len(names))
	for _, name := range names {
		ests[name] = est(name)
	}
	sorted := append([]string(nil), names...)
	sort.Slice(sorted, func(i, j int) bool {
		ei, ej := ests[sorted[i]], ests[sorted[j]]
		if ei != ej {
			return ei > ej
		}
		return sorted[i] < sorted[j]
	})
	shards := make([][]string, n)
	sums := make([]int64, n)
	for _, name := range sorted {
		best := 0
		for i := 1; i < n; i++ {
			if sums[i] < sums[best] {
				best = i
			}
		}
		shards[best] = append(shards[best], name)
		sums[best] += ests[name]
	}
	return shards
}
