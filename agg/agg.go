package agg

import "math"

// Aggregator 是一个分组聚合器。零值须为空聚合；Value 返回当前聚合结果。
type Aggregator interface {
	Add(v float64)
	// Remove 做增量撤回；recompute=true 表示仅凭聚合态无法撤回，
	// 调用方必须改用 Recompute 重建该组。
	Remove(v float64) (recompute bool)
	Recompute(members []float64)
	NeedsMembersOnDelete() bool
	Value() float64
}

// Kind 标识聚合器族。
type Kind int

const (
	Count Kind = iota
	Sum
	Min
	Max
	DistinctCount
)

// New 按族构造空聚合器。
func New(k Kind) Aggregator {
	switch k {
	case Count:
		return &countAgg{}
	case Sum:
		return &sumAgg{}
	case Min:
		return &minAgg{}
	case Max:
		return &maxAgg{}
	case DistinctCount:
		return &distinctAgg{vals: map[uint64]int{}}
	}
	return nil
}

type countAgg struct{ n int }

func (a *countAgg) Add(float64)             { a.n++ }
func (a *countAgg) Remove(float64) bool     { a.n--; return false }
func (a *countAgg) Recompute(m []float64)   { a.n = len(m) }
func (a *countAgg) NeedsMembersOnDelete() bool { return false }
func (a *countAgg) Value() float64          { return float64(a.n) }

type sumAgg struct{ s float64 }

func (a *sumAgg) Add(v float64)           { a.s += normZero(v) }
func (a *sumAgg) Remove(v float64) bool   { a.s -= normZero(v); return false }
func (a *sumAgg) Recompute(m []float64) {
	a.s = 0
	for _, v := range m {
		a.s += normZero(v)
	}
}
func (a *sumAgg) NeedsMembersOnDelete() bool { return false }
func (a *sumAgg) Value() float64            { return a.s }

// minAgg 额外记录当前最小值出现次数，归零才需重算。
type minAgg struct {
	cur float64
	cnt int
}

func (a *minAgg) Add(v float64) {
	v = normZero(v)
	if a.cnt == 0 || v < a.cur {
		a.cur, a.cnt = v, 1
	} else if equalVal(v, a.cur) {
		a.cnt++
	}
}
func (a *minAgg) Remove(v float64) bool {
	if a.cnt > 0 && equalVal(normZero(v), a.cur) {
		a.cnt--
		if a.cnt == 0 {
			return true
		}
	}
	return false
}
func (a *minAgg) Recompute(m []float64) {
	a.cur, a.cnt = 0, 0
	for _, v := range m {
		a.Add(v)
	}
}
func (a *minAgg) NeedsMembersOnDelete() bool { return true }
func (a *minAgg) Value() float64             { return a.cur }

type maxAgg struct {
	cur float64
	cnt int
}

func (a *maxAgg) Add(v float64) {
	v = normZero(v)
	if a.cnt == 0 || v > a.cur {
		a.cur, a.cnt = v, 1
	} else if equalVal(v, a.cur) {
		a.cnt++
	}
}
func (a *maxAgg) Remove(v float64) bool {
	if a.cnt > 0 && equalVal(normZero(v), a.cur) {
		a.cnt--
		if a.cnt == 0 {
			return true
		}
	}
	return false
}
func (a *maxAgg) Recompute(m []float64) {
	a.cur, a.cnt = 0, 0
	for _, v := range m {
		a.Add(v)
	}
}
func (a *maxAgg) NeedsMembersOnDelete() bool { return true }
func (a *maxAgg) Value() float64             { return a.cur }

type distinctAgg struct {
	vals map[uint64]int
}

func (a *distinctAgg) Add(v float64) {
	a.vals[math.Float64bits(normZero(v))]++
}
func (a *distinctAgg) Remove(float64) bool { return true }
func (a *distinctAgg) Recompute(m []float64) {
	a.vals = map[uint64]int{}
	for _, v := range m {
		a.Add(v)
	}
}
func (a *distinctAgg) NeedsMembersOnDelete() bool { return true }
func (a *distinctAgg) Value() float64             { return float64(len(a.vals)) }

func equalVal(x, y float64) bool { return normZero(x) == normZero(y) }

func normZero(f float64) float64 {
	if f == 0 {
		return 0
	}
	return f
}
