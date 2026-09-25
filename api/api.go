// Package api 对外提供按分区并行、确定性归并的聚合。依赖 merge。
package api

import (
	"errors"
	"fmt"

	"ontology/merge"
	"ontology/pagg"
)

// 三类可判定的哨兵错误，互不相同。
var (
	ErrPartitionNegative = errors.New("api: partition < 0")
	ErrPartitionTooLarge = errors.New("api: partition >= maxPartitions")
	ErrEmptyKey          = errors.New("api: empty key")
)

// Entry 是全局结果里的一行。
type Entry = pagg.Entry

// Aggregator 按分区并行聚合，Merge 时确定性归并。可并发使用。
type Aggregator struct {
	max   int
	parts []*pagg.Partial // New 之后不再变动，Add/Merge 只读写各分区内部状态
}

// New 建一个 maxPartitions 分区的聚合器。
func New(maxPartitions int) *Aggregator {
	parts := make([]*pagg.Partial, maxPartitions)
	for i := range parts {
		parts[i] = pagg.New()
	}
	return &Aggregator{max: maxPartitions, parts: parts}
}

// Add 校验并累加一条记录。任何校验失败都整体拒绝、不改任何状态。
func (a *Aggregator) Add(rec pagg.Record) error {
	switch {
	case rec.Partition < 0:
		return ErrPartitionNegative
	case rec.Partition >= a.max:
		return ErrPartitionTooLarge
	case rec.Key == "":
		return ErrEmptyKey
	}
	a.parts[rec.Partition].Add(rec)
	return nil
}

// Merge 归并全部分区的部分结果，返回按 key 全序的全局结果。
// 结果与分区完成顺序无关，每个 key 恰好出现一次。
func (a *Aggregator) Merge() []Entry {
	partials := make([][]pagg.Entry, len(a.parts))
	for i, p := range a.parts {
		partials[i] = p.Sorted()
	}
	return merge.Merge(partials)
}

// batch 是不动任何状态的批量重算：按 key 分组求和、按 key 升序。
func batch(recs []pagg.Record) []Entry {
	sum := map[string]int64{}
	for _, r := range recs {
		sum[r.Key] += r.Value
	}
	return merge.Merge([][]pagg.Entry{sortedEntries(sum)})
}

func sortedEntries(sum map[string]int64) []pagg.Entry {
	p := pagg.New()
	for k, v := range sum {
		p.Add(pagg.Record{Key: k, Value: v})
	}
	return p.Sorted()
}

// SelfCheck 对一组内置记录序列核验四条不变量，全部通过返回 nil。
// 只在内部新建聚合器上做，不碰调用方状态。
func SelfCheck() error {
	recs := []pagg.Record{
		{Partition: 0, Key: "a", Value: 5}, {Partition: 1, Key: "a", Value: 1},
		{Partition: 2, Key: "b", Value: 6}, {Partition: 0, Key: "a", Value: 3},
		{Partition: 1, Key: "c", Value: 4}, {Partition: 2, Key: "a", Value: 2},
	}
	agg := New(3)
	for _, r := range recs {
		if err := agg.Add(r); err != nil {
			return fmt.Errorf("selfcheck add: %w", err)
		}
	}
	got := agg.Merge()
	// 不变量 1+3：与批量重算逐 key 相同，每记录恰计一次
	if fmt.Sprint(got) != fmt.Sprint(batch(recs)) {
		return errors.New("selfcheck: merge != batch recompute")
	}
	// 不变量 2：任意分区完成顺序（此处枚举全部排列）结果逐字段相同
	for _, perm := range [][3]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}} {
		var parts [][]pagg.Entry
		for _, i := range perm {
			parts = append(parts, agg.parts[i].Sorted())
		}
		if fmt.Sprint(merge.Merge(parts)) != fmt.Sprint(got) {
			return errors.New("selfcheck: result depends on completion order")
		}
	}
	// 不变量 4：三类拒绝互不相同且不留痕
	before := fmt.Sprint(agg.Merge())
	errs := []error{
		agg.Add(pagg.Record{Partition: -1, Key: "x"}),
		agg.Add(pagg.Record{Partition: 3, Key: "x"}),
		agg.Add(pagg.Record{Partition: 0, Key: ""}),
	}
	if errs[0] != ErrPartitionNegative || errs[1] != ErrPartitionTooLarge || errs[2] != ErrEmptyKey {
		return errors.New("selfcheck: wrong sentinel errors")
	}
	if fmt.Sprint(agg.Merge()) != before {
		return errors.New("selfcheck: rejected add changed state")
	}
	return nil
}
