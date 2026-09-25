package ranking

import (
	"math"
	"sort"
)

// Rank 对一批行按分区分别计算 ROW_NUMBER / RANK / DENSE_RANK 并一次返回。
//
// 输入切片与行结构不会被修改；返回的 Result.Rows 是新分配的切片。
// 分区键为 nil 或排序值为 NaN 的行被跳过并计入对应计数。
func Rank(rows []Row, opts Options) Result {
	compare := opts.Compare
	if compare == nil {
		compare = defaultCompare
	}

	groups := make(map[string][]Row)
	var res Result
	for _, r := range rows {
		if r.PartitionKey == nil {
			res.SkippedNilPartition++
			continue
		}
		if math.IsNaN(r.SortValue) {
			res.SkippedNaN++
			continue
		}
		key := *r.PartitionKey
		groups[key] = append(groups[key], r)
	}

	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	total := 0
	for _, k := range keys {
		total += len(groups[k])
	}
	res.Rows = make([]RankedRow, 0, total)
	for _, k := range keys {
		res.Rows = append(res.Rows, rankPartition(groups[k], compare, opts.Descending)...)
	}
	return res
}

// rankPartition 对单个分区内的行排序并赋予三种排名。
// 不修改输入切片，返回新分配的切片。
func rankPartition(rows []Row, compare func(a, b float64) int, descending bool) []RankedRow {
	sorted := make([]Row, len(rows))
	copy(sorted, rows)
	sort.Slice(sorted, func(i, j int) bool {
		return less(sorted[i], sorted[j], compare, descending)
	})

	out := make([]RankedRow, len(sorted))
	for i, r := range sorted {
		out[i].Row = r
		out[i].RowNumber = i + 1
		if i > 0 && compare(sorted[i-1].SortValue, r.SortValue) == 0 {
			out[i].Rank = out[i-1].Rank
			out[i].DenseRank = out[i-1].DenseRank
		} else {
			out[i].Rank = i + 1
			out[i].DenseRank = 1
			if i > 0 {
				out[i].DenseRank = out[i-1].DenseRank + 1
			}
		}
	}
	return out
}
