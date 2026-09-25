package rank

import (
	"math"
	"sort"
)

// entry 是一个已通过校验的分区内部条目（与输入结构完全解耦）。
type entry struct {
	value float64
	id    string
}

// RankRows 按分区计算 ROW_NUMBER / RANK / DENSE_RANK。
// 输入 rows 与其每个元素均不会被修改；返回的 Rankings 为新分配切片。
// Partition 为 nil 或 Value 为 NaN 的行被拒绝，并计入 Stats 跳过计数。
func RankRows(rows []Row, dir Direction) *Result {
	res := &Result{Rankings: make([]Ranking, 0, len(rows))}
	if dir != Desc {
		dir = Asc
	}

	groups := make(map[string][]entry)
	keys := make([]string, 0)
	for i := range rows {
		r := rows[i]
		skip := false
		if r.Partition == nil {
			res.Stats.SkippedNilPartition++
			skip = true
		}
		if math.IsNaN(r.Value) {
			res.Stats.SkippedNaN++
			skip = true
		}
		if skip {
			continue
		}
		key := *r.Partition
		if _, ok := groups[key]; !ok {
			keys = append(keys, key)
		}
		groups[key] = append(groups[key], entry{value: r.Value, id: r.ID})
	}
	sort.Strings(keys)

	for _, key := range keys {
		s := &countedSorter{entries: groups[key], dir: dir}
		sort.Sort(s)
		res.Stats.Comparisons += s.count

		dense := 0
		for i := range s.entries {
			if i == 0 || !sameValue(s.entries[i-1].value, s.entries[i].value) {
				dense++
			}
			e := s.entries[i]
			res.Rankings = append(res.Rankings, Ranking{
				ID:        e.id,
				Partition: key,
				Value:     e.value,
				RowNumber: i + 1,
				Rank:      dense,
				DenseRank: dense,
			})
		}
		rankPartition(res.Rankings, len(s.entries))
	}
	return res
}

// rankPartition 修正最近 partLen 个结果中的 RANK：
// 并列行取该组首行的位置（即并列后跳号的语义）。
func rankPartition(out []Ranking, partLen int) {
	base := len(out) - partLen
	for start := 0; start < partLen; {
		end := start + 1
		for end < partLen && out[base+end].Rank == out[base+start].Rank {
			end++
		}
		rank := out[base+start].RowNumber
		for k := start; k < end; k++ {
			out[base+k].Rank = rank
		}
		start = end
	}
}
