// Package agg 定义聚合器族及其增量撤回能力声明。
package agg

import "math/big"

// Kind 标识一种聚合器。
type Kind int

const (
	Count Kind = iota
	Sum
	Min
	Max
	DistinctCount
	// NumKinds 是聚合器种数。
	NumKinds
)

func (k Kind) String() string {
	switch k {
	case Count:
		return "Count"
	case Sum:
		return "Sum"
	case Min:
		return "Min"
	case Max:
		return "Max"
	case DistinctCount:
		return "DistinctCount"
	}
	return "Unknown"
}

// All 返回全部聚合器。
func All() []Kind {
	return []Kind{Count, Sum, Min, Max, DistinctCount}
}

// InsertIncremental 声明插入是否可增量维护（五种聚合插入都可增量）。
func (k Kind) InsertIncremental() bool { return true }

// DeleteIncremental 声明删除是否可增量撤回（纯标量逆运算，无需组成员）。
// 推导见 DESIGN.md 第 2 节：Count/Sum 可减，Min/Max/DistinctCount 不可。
func (k Kind) DeleteIncremental() bool { return k == Count || k == Sum }

// NeedsMembersOnDelete 声明删除时是否需要该组成员（或成员级状态）。
// view 依此决定是否触发 Recompute 阶段。
func (k Kind) NeedsMembersOnDelete() bool { return !k.DeleteIncremental() }

// sumPrec 足以精确表示任意 float64 多重集之和（指数跨度约 2100 位 + 进位余量），
// 因此增量加减与全量重算得到完全相同的精确和，舍入后 IEEE754 位级相等。
const sumPrec = 4096

// Summer 是精确求和器，支持可逆的增量加减。
type Summer struct {
	acc big.Float
}

func (s *Summer) ensure() {
	if s.acc.Prec() == 0 {
		s.acc.SetPrec(sumPrec)
	}
}

// Add 累加一个值。
func (s *Summer) Add(v float64) {
	s.ensure()
	s.acc.Add(&s.acc, big.NewFloat(v))
}

// Sub 精确撤销一个已累加的值。
func (s *Summer) Sub(v float64) {
	s.ensure()
	s.acc.Sub(&s.acc, big.NewFloat(v))
}

// Value 返回舍入到 float64 的和。
func (s *Summer) Value() float64 {
	f, _ := s.acc.Float64()
	return f
}

// MinOf 全组重算最小值；空集返回 ok=false。
func MinOf(vals []float64) (m float64, ok bool) {
	if len(vals) == 0 {
		return 0, false
	}
	m = vals[0]
	for _, v := range vals[1:] {
		if v < m {
			m = v
		}
	}
	return m, true
}

// MaxOf 全组重算最大值；空集返回 ok=false。
func MaxOf(vals []float64) (m float64, ok bool) {
	if len(vals) == 0 {
		return 0, false
	}
	m = vals[0]
	for _, v := range vals[1:] {
		if v > m {
			m = v
		}
	}
	return m, true
}
