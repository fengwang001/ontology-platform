package ontology

import "slices"

// compareRows 定义榜单的全序：分数高者排前；分数相同时按键的字典序升序。
// 返回负数表示 a 排在 b 前面，0 表示键相同（同一行），正数反之。
// 键在存活集合中唯一，因此该比较是严格全序，排序结果确定。
func compareRows(a, b Row) int {
	if a.Score != b.Score {
		if a.Score > b.Score {
			return -1
		}
		return 1
	}
	if a.Key != b.Key {
		if a.Key < b.Key {
			return -1
		}
		return 1
	}
	return 0
}

// computeRanks 将存活行按 compareRows 排序，返回名次从 1 开始的完整条目列表。
// 入参 map 不会被修改；返回切片始终非 nil。
func computeRanks(live map[string]Score) []Entry {
	rows := make([]Row, 0, len(live))
	for key, score := range live {
		rows = append(rows, Row{Key: key, Score: score})
	}
	slices.SortFunc(rows, compareRows)

	entries := make([]Entry, 0, len(rows))
	for i, r := range rows {
		entries = append(entries, Entry{
			Rank:  i + 1,
			Key:   r.Key,
			Score: r.Score,
		})
	}
	return entries
}
