// Package agg 定义可增量（及可撤回）维护的分组聚合器族。
//
// 每个聚合器必须显式回答两个问题：
//   - Insert/Delete 单条值能否在不访问组内其他成员的情况下增量更新；
//   - 删除时是否需要该组全部成员（NeedsMembers）。
//
// view 仅在相应聚合器声明需要成员且删除命中其敏感条件时触发 Recompute。
package agg

// Kind 标识聚合器种类。
type Kind uint8

const (
	Count Kind = 1 + iota
	Sum
	Min
	Max
	DistinctCount
)

// Aggregator 是单个组内单个聚合的状态机。
// 所有方法仅在 view 的写锁内被调用，本身不处理并发。
type Aggregator interface {
	Kind() Kind
	// Add 增量插入一个值。
	Add(v float64)
	// Remove 增量删除一个值；sensitive=true 表示仅靠聚合状态无法得出新结果
	// （如删到当前极值），调用方随后必须 Recompute。
	Remove(v float64) (sensitive bool)
	// NeedsMembers 声明删除时是否可能需要组内全部成员。
	NeedsMembers() bool
	// Recompute 按组成员值全量重算并写回本聚合器。
	Recompute(values func(yield func(float64) bool))
	// Value 返回当前聚合结果（计数类按 float64 承载）。
	Value() float64
	// Reset 清空为无成员状态。
	Reset()
}

// All 返回一组全新的五个聚合器。
func All() []Aggregator {
	return []Aggregator{
		&countAgg{}, &sumAgg{}, &minAgg{}, &maxAgg{}, newDistinct(),
	}
}

// ByKind 在聚合器切片中查找指定种类。
func ByKind(aggs []Aggregator, k Kind) Aggregator {
	for _, a := range aggs {
		if a.Kind() == k {
			return a
		}
	}
	return nil
}
