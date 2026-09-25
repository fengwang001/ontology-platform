package ranking

import (
	"math"
	"sort"
)

// Rank 对一批行按分区分别计算 ROW_NUMBER / RANK / DENSE_RANK 并一次返回。
//
// 输入切片与行结构不会被修改；返回的切片是新分配的。
// nil 分区键或 NaN 排序值的行被拒绝并计入 Skipped。
func Rank(rows []Row, opts Options) Result {
	compare := resolveCompare(opts)
	parts := partition(rows)
	keys := sortedKeys(parts)

	total := 0
	for _, key := range keys {
		total += len(parts[key])
	}
	out := Result{
		Rows:    make([]RankedRow, 0, total),
		Skipped: len(rows) - total,
	}
	for _, key := range keys {
		out.Rows = append(out.Rows, rankPartition(parts[key], compare)...)
	}
	return out
}

// partition 按分区键分组，拒绝 nil 分区键与 NaN 排序值的行。
// 返回的是新分配的切片，输入不被修改。
func partition(rows []Row) map[string][]Row {
	parts := make(map[string][]Row)
	for _, row := range rows {
		if row.Partition == nil || math.IsNaN(row.Value) {
			continue
		}
		key := *row.Partition
		parts[key] = append(parts[key], row)
	}
	return parts
}

// sortedKeys 返回按字典序排列的分区键，保证分区间的输出顺序确定。
func sortedKeys(parts map[string][]Row) []string {
	keys := make([]string, 0, len(parts))
	for key := range parts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// rankPartition 对单个分区排序并赋三种排名，各自从 1 开始。
func rankPartition(rows []Row, compare func(a, b Row) int) []RankedRow {
	sorted := make([]Row, len(rows))
	copy(sorted, rows)
	sort.SliceStable(sorted, func(i, j int) bool {
		return compare(sorted[i], sorted[j]) < 0
	})

	ranked := make([]RankedRow, len(sorted))
	for i, row := range sorted {
		ranked[i] = RankedRow{Row: row, RowNumber: i + 1, Rank: i + 1, DenseRank: i + 1}
		if i > 0 && compareValue(sorted[i-1].Value, row.Value) == 0 {
			ranked[i].Rank = ranked[i-1].Rank
			ranked[i].DenseRank = ranked[i-1].DenseRank
		} else if i > 0 {
			ranked[i].DenseRank = ranked[i-1].DenseRank + 1
		}
	}
	return ranked
}
