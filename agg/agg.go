// Package agg 定义可增量维护的聚合器族。
package agg

// Kind 标识聚合种类。
type Kind int

const (
	Count Kind = iota
	Sum
	Min
	Max
	DistinctCount
)

// Names 按枚举顺序给出聚合名。
var Names = [...]string{"count", "sum", "min", "max", "distinct_count"}

func (k Kind) String() string { return Names[k] }

// Aggregator 是单组单个聚合的状态机。
//
// 删除语义：NeedsMembersOnDelete 声明该聚合是否可能无法仅凭
// (当前聚合态, 被删值) 完成删除；NeedsRecomputeOnDelete 在给定具体
// 被删值时给出最终判定，为 true 时 view 必须用该组成员调 Build 重算。
type Aggregator interface {
	Kind() Kind
	NeedsMembersOnDelete() bool
	NeedsRecomputeOnDelete(v float64) bool
	Insert(v float64)
	Delete(v float64)
	Build(members map[string]float64)
	Value() float64
	Reset()
}

// NewSet 返回一组五个全新聚合器，顺序与 Kind 枚举一致。
func NewSet() []Aggregator {
	return []Aggregator{
		&countAgg{}, &sumAgg{}, &minAgg{empty: true},
		&maxAgg{empty: true}, &distinctAgg{seen: map[float64]struct{}{}},
	}
}

// eq 把 +0 与 -0 视为相等，NaN 不参与（入口已拒绝）。
func eq(a, b float64) bool { return a == b }
