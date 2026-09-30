package index

import "fmt"

// Bound 是区间边界：带索引序比较的键，加上开闭标记。
// Unbounded 表示该侧无界。
type Bound struct {
	Key       []Value
	Inclusive bool
	Unbounded bool
}

func (b Bound) String() string {
	if b.Unbounded {
		return "inf"
	}
	s := "("
	if b.Inclusive {
		s = "["
	}
	for i, v := range b.Key {
		if i > 0 {
			s += ","
		}
		s += v.String()
	}
	if b.Inclusive {
		return s + "]"
	}
	return s + ")"
}

// Interval 是索引序上的一个键区间 [Lo, Hi]。
type Interval struct {
	Lo Bound
	Hi Bound
}

func (iv Interval) String() string {
	lo, hi := "(-inf", "+inf)"
	if !iv.Lo.Unbounded {
		lo = iv.Lo.String()
	}
	if !iv.Hi.Unbounded {
		hi = iv.Hi.String()
	}
	return fmt.Sprintf("%s, %s", lo, hi)
}

// Plan 是一次访问路径推导的结果。
type Plan struct {
	Intervals []Interval // 按索引序排列、互不重叠
	Residual  []Cond     // 未并入区间的条件，扫描时逐条过滤
	Empty     bool       // 条件矛盾：空区间集合，考察零条
}

func (p Plan) String() string {
	if p.Empty {
		return fmt.Sprintf("Plan{EMPTY (contradictory conditions), residual=%v}", p.Residual)
	}
	s := "Plan{intervals=["
	for i, iv := range p.Intervals {
		if i > 0 {
			s += " "
		}
		s += iv.String()
	}
	s += "]"
	if len(p.Residual) > 0 {
		s += fmt.Sprintf(" residual=%v", p.Residual)
	}
	return s + "}"
}
