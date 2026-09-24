// Package report 汇总扇出结果、合并值与可信度，产出带标注的报告。
package report

import (
	"errors"
	"slices"

	"ontology/combine"
	"ontology/confidence"
	"ontology/fanout"
	"ontology/shard"
)

// 可判定哨兵错误。
var (
	ErrNoShards  = errors.New("report: no shards")
	ErrAllFailed = errors.New("report: all shards failed")
)

// ShardStatus 记录单个分片的结局。
type ShardStatus struct {
	ID     string
	Status fanout.Status
	Err    error
}

// Report 是一次聚合查询的完整报告。
type Report struct {
	Combined combine.Result
	Count    confidence.Assessment
	Sum      confidence.Assessment
	Min      confidence.Assessment
	Max      confidence.Assessment
	TopK     confidence.Assessment
	Missing  []string // 缺失分片 ID，升序
	Statuses []ShardStatus
}

// Build 由分片清单与扇出结果构建报告。
// 零分片返回 ErrNoShards；无成功分片返回 ErrAllFailed，
// 绝不把零值标注为「精确」。
func Build(shards []shard.Shard, results []fanout.Result, k int) (Report, error) {
	if len(shards) == 0 {
		return Report{}, ErrNoShards
	}
	bounds := map[string]float64{}
	for _, s := range shards {
		if _, ok := bounds[s.ID()]; !ok {
			bounds[s.ID()] = s.Bound()
		}
	}
	var rep Report
	var oks []shard.Response
	missingBound := 0.0
	for _, r := range results {
		rep.Statuses = append(rep.Statuses, ShardStatus{ID: r.ID, Status: r.Status, Err: r.Err})
		if r.Status == fanout.StatusOK {
			oks = append(oks, r.Resp)
		} else {
			rep.Missing = append(rep.Missing, r.ID)
			missingBound += bounds[r.ID]
		}
	}
	if len(oks) == 0 {
		return Report{}, ErrAllFailed
	}
	slices.Sort(rep.Missing)
	rep.Combined = combine.Merge(oks, k)
	in := confidence.Input{
		Total: len(results), OK: len(oks),
		MissingBound: missingBound,
		TopK:         rep.Combined.TopK,
	}
	in.Value = float64(rep.Combined.Count)
	rep.Count = confidence.AssessCount(in)
	in.Value = rep.Combined.Sum
	rep.Sum = confidence.AssessSum(in)
	in.Value = rep.Combined.Min
	rep.Min = confidence.AssessMin(in)
	in.Value = rep.Combined.Max
	rep.Max = confidence.AssessMax(in)
	rep.TopK = confidence.AssessTopK(in)
	return rep, nil
}
