package byterange

import "sort"

// mergeRanges 将区间按起点升序排序，并合并重叠或相邻的区间。
// 结果按起点升序且互不相交，不修改入参切片。
func mergeRanges(ranges []Range) []Range {
	sorted := make([]Range, len(ranges))
	copy(sorted, ranges)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Start != sorted[j].Start {
			return sorted[i].Start < sorted[j].Start
		}
		return sorted[i].End < sorted[j].End
	})
	merged := make([]Range, 0, len(sorted))
	for _, r := range sorted {
		last := len(merged) - 1
		if last >= 0 && r.Start <= merged[last].End+1 {
			if r.End > merged[last].End {
				merged[last].End = r.End
			}
			continue
		}
		merged = append(merged, r)
	}
	return merged
}
