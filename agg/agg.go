// Package agg 定义可增量维护的聚合器族。
package agg

// Aggregator 维护单个分组上的一个聚合。
type Aggregator interface {
	// Add 增量插入一条成员值。
	Add(v float64)
	// Remove 增量删除一条成员值。
	// 返回 false 表示仅凭聚合状态无法撤回，调用方必须全组重算。
	Remove(v float64) bool
	// NeedMembersOnDelete 声明删除时是否需要该组全部成员（触发 Recompute）。
	NeedMembersOnDelete() bool
	// Value 返回当前聚合结果；空组返回 (0,false)。
	Value() (float64, bool)
	// Reset 清空聚合状态，供重算使用。
	Reset()
}

// Count 计数：删除直接减，永不需要成员。
type Count struct{ N int64 }

func (a *Count) Add(float64)               { a.N++ }
func (a *Count) Remove(float64) bool       { a.N--; return true }
func (a *Count) NeedMembersOnDelete() bool { return false }
func (a *Count) Value() (float64, bool) {
	if a.N == 0 {
		return 0, false
	}
	return float64(a.N), true
}
func (a *Count) Reset() { a.N = 0 }

// Sum 求和：删除直接减，永不需要成员。
type Sum struct {
	S float64
	N int64
}

func (a *Sum) Add(v float64)             { a.S += v; a.N++ }
func (a *Sum) Remove(v float64) bool     { a.S -= v; a.N--; return true }
func (a *Sum) NeedMembersOnDelete() bool { return false }
func (a *Sum) Value() (float64, bool)    { return a.S, a.N > 0 }
func (a *Sum) Reset()                    { a.S, a.N = 0, 0 }

// Min 最小值：仅当删掉的值等于当前极值时无法反推，需要成员重算。
type Min struct {
	V   float64
	Set bool
}

func (a *Min) Add(v float64) {
	if !a.Set || v < a.V {
		a.V, a.Set = v, true
	}
}
func (a *Min) Remove(v float64) bool {
	if !a.Set || v == a.V {
		return false // 删到当前极值，标量状态无法推出次小值
	}
	return true
}
func (a *Min) NeedMembersOnDelete() bool { return true }
func (a *Min) Value() (float64, bool)    { return a.V, a.Set }
func (a *Min) Reset()                    { a.V, a.Set = 0, false }

// Max 最大值：与 Min 对称。
type Max struct {
	V   float64
	Set bool
}

func (a *Max) Add(v float64) {
	if !a.Set || v > a.V {
		a.V, a.Set = v, true
	}
}
func (a *Max) Remove(v float64) bool {
	if !a.Set || v == a.V {
		return false
	}
	return true
}
func (a *Max) NeedMembersOnDelete() bool { return true }
func (a *Max) Value() (float64, bool)    { return a.V, a.Set }
func (a *Max) Reset()                    { a.V, a.Set = 0, false }

// DistinctCount 去重计数：删除某值后是否减 1 取决于是否仍有成员持有它。
type DistinctCount struct{ M map[float64]int }

func (a *DistinctCount) Add(v float64) {
	if a.M == nil {
		a.M = map[float64]int{}
	}
	a.M[v]++
}
func (a *DistinctCount) Remove(v float64) bool {
	if a.M == nil {
		return false
	}
	n := a.M[v]
	if n <= 1 {
		return false // 最后一个持有者，删除后基数变化需成员信息，走重算
	}
	a.M[v] = n - 1
	return true
}
func (a *DistinctCount) NeedMembersOnDelete() bool { return true }
func (a *DistinctCount) Value() (float64, bool) {
	if len(a.M) == 0 {
		return 0, false
	}
	return float64(len(a.M)), true
}
func (a *DistinctCount) Reset() { a.M = nil }

// Kind 标识聚合器种类。
type Kind int

const (
	KCount Kind = iota
	KSum
	KMin
	KMax
	KDistinctCount
)

// New 按种类创建聚合器。
func (k Kind) New() Aggregator {
	switch k {
	case KCount:
		return &Count{}
	case KSum:
		return &Sum{}
	case KMin:
		return &Min{}
	case KMax:
		return &Max{}
	case KDistinctCount:
		return &DistinctCount{}
	default:
		return nil
	}
}

// AllKinds 列出全部聚合器种类。
func AllKinds() []Kind {
	return []Kind{KCount, KSum, KMin, KMax, KDistinctCount}
}

func (k Kind) String() string {
	switch k {
	case KCount:
		return "Count"
	case KSum:
		return "Sum"
	case KMin:
		return "Min"
	case KMax:
		return "Max"
	case KDistinctCount:
		return "DistinctCount"
	}
	return "?"
}
