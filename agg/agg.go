// Package agg 提供五个聚合器。每个聚合器显式声明删除时是否需要组成员，
// 状态只保留聚合值本身（DistinctCount 另持有一张仅服务插入路径的计数表）。
package agg

// Aggregator 维护单个分组的一维聚合。
type Aggregator interface {
	Name() string
	// NeedsMembers 静态声明：删除时是否需要该组成员（触发 Recompute 的可能）。
	NeedsMembers() bool
	// Add 应用一条插入贡献，所有聚合器都可增量完成。
	Add(v float64)
	// Retract 尝试增量撤回一条删除贡献；返回 false 表示必须由 Recompute 重建。
	Retract(v float64) bool
	// Recompute 从该组当前成员全量重建聚合状态。
	Recompute(members []float64)
	// Value 返回当前聚合值。
	Value() float64
}

// Family 返回一组全新的五个聚合器实例（每组一份）。
func Family() []Aggregator {
	return []Aggregator{&Count{}, &Sum{}, &Min{}, &Max{}, &DistinctCount{}}
}

// Count 计数：删除的贡献与被删记录内容无关，可直接减。
type Count struct{ n float64 }

func (a *Count) Name() string             { return "Count" }
func (a *Count) NeedsMembers() bool       { return false }
func (a *Count) Add(float64)              { a.n++ }
func (a *Count) Retract(float64) bool     { a.n--; return true }
func (a *Count) Recompute(m []float64)    { a.n = float64(len(m)) }
func (a *Count) Value() float64           { return a.n }

// Sum 求和：加法有逆元，删除可精确对冲。
type Sum struct{ s float64 }

func (a *Sum) Name() string          { return "Sum" }
func (a *Sum) NeedsMembers() bool    { return false }
func (a *Sum) Add(v float64)         { a.s += v }
func (a *Sum) Retract(v float64) bool { a.s -= v; return true }
func (a *Sum) Recompute(m []float64) {
	a.s = 0
	for _, v := range m {
		a.s += v
	}
}
func (a *Sum) Value() float64 { return a.s }

// Min 最小值：插入可增量；删除仅当被删值就是当前极值时无法从状态反推新极值。
type Min struct {
	m   float64
	set bool
}

func (a *Min) Name() string       { return "Min" }
func (a *Min) NeedsMembers() bool { return true }
func (a *Min) Add(v float64) {
	if !a.set || v < a.m {
		a.m = v
	}
	a.set = true
}
func (a *Min) Retract(v float64) bool { return v != a.m }
func (a *Min) Recompute(m []float64) {
	a.set = false
	for _, v := range m {
		a.Add(v)
	}
}
func (a *Min) Value() float64 { return a.m }

// Max 最大值：与 Min 对称。
type Max struct {
	m   float64
	set bool
}

func (a *Max) Name() string       { return "Max" }
func (a *Max) NeedsMembers() bool { return true }
func (a *Max) Add(v float64) {
	if !a.set || v > a.m {
		a.m = v
	}
	a.set = true
}
func (a *Max) Retract(v float64) bool { return v != a.m }
func (a *Max) Recompute(m []float64) {
	a.set = false
	for _, v := range m {
		a.Add(v)
	}
}
func (a *Max) Value() float64 { return a.m }

// DistinctCount 去重计数：插入侧用计数表增量维护。删除时若计数表显示被删值
// 仍被其他记录持有，则计数不变、可增量撤回；若是最后持有者，计数必然变化，
// 按契约删除路径不依赖该表（表可被裁剪），需要回到成员重算确认。
type DistinctCount struct {
	n    int
	refs map[float64]int
}

func (a *DistinctCount) Name() string       { return "DistinctCount" }
func (a *DistinctCount) NeedsMembers() bool { return true }
func (a *DistinctCount) Add(v float64) {
	if a.refs == nil {
		a.refs = make(map[float64]int)
	}
	a.refs[v]++
	if a.refs[v] == 1 {
		a.n++
	}
}
func (a *DistinctCount) Retract(v float64) bool {
	if a.refs[v] > 1 {
		a.refs[v]--
		return true
	}
	return false
}
func (a *DistinctCount) Recompute(m []float64) {
	a.refs = make(map[float64]int, len(m))
	for _, v := range m {
		a.refs[v]++
	}
	a.n = len(a.refs)
}
func (a *DistinctCount) Value() float64 { return float64(a.n) }
