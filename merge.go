package ontology

// Merge 把两个独立累加器合并成一个新累加器，不修改 a 或 b。
//
// 合并公式写成对 (a, b) 对称的形式，Merge(a, b) 与 Merge(b, a)
// 的结果逐位相同；合并空累加器是恒等操作，结果与另一方逐位一致；
// 两个空累加器合并仍为空。任一方统计量不可用则合并结果也不可用，
// 双方的跳过计数累加到结果中。
func Merge(a, b *Accumulator) *Accumulator {
	ca, ma, m2a, sa, ua := a.snapshot()
	cb, mb, m2b, sb, ub := b.snapshot()

	out := &Accumulator{
		count:    ca + cb,
		skipped:  sa + sb,
		unusable: ua || ub,
	}

	// 恒等情形：任一方为空时直接逐位拷贝另一方状态，
	// 不经过任何浮点运算，保证合并前后逐位不变。
	switch {
	case ca == 0:
		out.mean = mb
		out.m2 = m2b
		return out
	case cb == 0:
		out.mean = ma
		out.m2 = m2a
		return out
	case ua || ub:
		// 统计量已不可用，均值与 m2 无意义，保持零值即可，
		// 读取接口会返回 ErrUnavailable。
		return out
	}

	n := float64(out.count)
	fa := float64(ca)
	fb := float64(cb)
	delta := mb - ma
	out.mean = (ma*fa + mb*fb) / n
	out.m2 = m2a + m2b + delta*delta*fa*fb/n
	return out
}
