package ontology

import "math"

// Merge 把两个独立累加器合并为一个新累加器，不修改 a 或 b。
//
// 合并公式（Chan et al. 的并行方差合并，与 Welford 递推等价）：
//
//	delta = b.mean - a.mean
//	mean  = a.mean + delta * nb / n
//	m2    = a.m2 + b.m2 + delta^2 * na * nb / n
//
// 为保证 Merge(a, b) 与 Merge(b, a) 逐位相同，合并前先把两个操作数
// 按 (count, mean, m2, skipped) 的规范顺序排序，再套用上述公式。
// 合并空累加器是恒等操作；任一源已被 ±Inf 污染时结果同样不可用。
func Merge(a, b *Accumulator) *Accumulator {
	x := a.snapshot()
	y := b.snapshot()
	if x.count == 0 {
		y.skipped += x.skipped
		return fromState(y)
	}
	if y.count == 0 {
		x.skipped += y.skipped
		return fromState(x)
	}
	if !canonicalFirst(x, y) {
		x, y = y, x
	}
	n := x.count + y.count
	out := &Accumulator{
		count:   n,
		skipped: x.skipped + y.skipped,
		broken:  x.broken || y.broken,
	}
	if out.broken {
		return out
	}
	nf := float64(n)
	delta := y.mean - x.mean
	out.mean = x.mean + delta*float64(y.count)/nf
	out.m2 = x.m2 + y.m2 + delta*delta*float64(x.count)*float64(y.count)/nf
	return out
}

func fromState(s state) *Accumulator {
	return &Accumulator{
		count:   s.count,
		mean:    s.mean,
		m2:      s.m2,
		skipped: s.skipped,
		broken:  s.broken,
	}
}

// canonicalFirst 报告 x 是否应在规范顺序中排在 y 之前。
// 完全相等时顺序不影响结果（delta 为 0），返回 true。
func canonicalFirst(x, y state) bool {
	if x.count != y.count {
		return x.count < y.count
	}
	if x.mean != y.mean {
		return math.Float64bits(x.mean) < math.Float64bits(y.mean)
	}
	if x.m2 != y.m2 {
		return math.Float64bits(x.m2) < math.Float64bits(y.m2)
	}
	return x.skipped <= y.skipped
}
