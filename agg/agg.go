// Package agg 提供增量聚合器族。每个聚合器显式声明删除时是否需要
// 回到组成员重算（见 DESIGN.md 的推导）。
package agg

import "math"

// Aggregator 是单组单指标的增量聚合器。
type Aggregator interface {
	// Name 是聚合器标识："count"/"sum"/"min"/"max"/"distinct"。
	Name() string
	// IncrementalDelete 声明删除时是否无需组成员即可撤回。
	IncrementalDelete() bool
	// Add 处理一条插入（所有聚合器都必须增量支持）。
	Add(v float64)
	// Sub 增量撤回一条删除；仅当 IncrementalDelete 为真时会被调用。
	Sub(v float64)
	// RemoveCausesRecompute 报告删除值 v 是否迫使回到成员重算；
	// 仅当 IncrementalDelete 为假时会被询问。
	RemoveCausesRecompute(v float64) bool
	// Recompute 从该组当前成员全量重建状态。
	Recompute(members []float64)
	// Value 返回当前聚合值（计数类以 float64 表示）。
	Value() float64
}

// Values 是一组五个聚合结果的快照。
type Values struct {
	Count    int64
	Sum      float64
	Min      float64
	Max      float64
	Distinct int64
}

// EqualBits 逐字段比对，浮点字段按 IEEE754 位级相等。
func EqualBits(a, b Values) bool {
	return a.Count == b.Count &&
		math.Float64bits(a.Sum) == math.Float64bits(b.Sum) &&
		math.Float64bits(a.Min) == math.Float64bits(b.Min) &&
		math.Float64bits(a.Max) == math.Float64bits(b.Max) &&
		a.Distinct == b.Distinct
}

// All 构造五个聚合器各一个实例。
func All() []Aggregator {
	return []Aggregator{
		new(count), new(sum), newMin(), newMax(), newDistinct(),
	}
}

// Snapshot 从一组聚合器收集快照。
func Snapshot(as []Aggregator) Values {
	var v Values
	for _, a := range as {
		switch a.Name() {
		case "count":
			v.Count = int64(a.Value())
		case "sum":
			v.Sum = a.Value()
		case "min":
			v.Min = a.Value()
		case "max":
			v.Max = a.Value()
		case "distinct":
			v.Distinct = int64(a.Value())
		}
	}
	return v
}
