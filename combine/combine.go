// Package combine 把若干成功分片的结果合并为五种聚合。
package combine

import (
	"slices"

	"ontology/shard"
)

// TopEntry 是 TopK 榜单中的一项：Score 为该 ID 跨分片的分数和。
type TopEntry struct {
	ID    string
	Score float64
}

// Result 是五种聚合的合并结果。Empty 为 true 表示没有任何记录
// （区别于分片失败：这是「全部成功但都返回空」的合法结果）。
type Result struct {
	Count int
	Sum   float64
	Min   float64
	Max   float64
	TopK  []TopEntry
	Empty bool
}

// Merge 合并成功分片的响应。累加与排序均与输入顺序无关：
// TopK 按得分降序、并列按 ID 升序，结果对任意到达顺序逐字节相同。
// k <= 0 时返回空榜单；k 大于总条目数时返回全部条目。
func Merge(responses []shard.Response, k int) Result {
	var res Result
	res.Empty = true
	totals := map[string]float64{}
	first := true
	for _, resp := range responses {
		res.Count += len(resp.Records)
		for _, rec := range resp.Records {
			res.Empty = false
			res.Sum += rec.Value
			if first || rec.Value < res.Min {
				res.Min = rec.Value
			}
			if first || rec.Value > res.Max {
				res.Max = rec.Value
			}
			first = false
			totals[rec.ID] += rec.Value
		}
	}
	entries := make([]TopEntry, 0, len(totals))
	for id, score := range totals {
		entries = append(entries, TopEntry{ID: id, Score: score})
	}
	slices.SortFunc(entries, func(a, b TopEntry) int {
		if a.Score != b.Score {
			if a.Score > b.Score {
				return -1
			}
			return 1
		}
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	if k > len(entries) {
		k = len(entries)
	}
	if k > 0 {
		res.TopK = entries[:k]
	}
	return res
}
