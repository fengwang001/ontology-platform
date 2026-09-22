package ontology

// Merge 把两个独立累加器合并为一个新累加器，不修改任何一个源。
//
// 采用 Chan 等人的并行方差合并公式：
//
//	n     = na + nb
//	delta = meanB - meanA
//	mean  = meanA*(na/n) + meanB*(nb/n)
//	m2    = m2A + m2B + delta^2 * na*nb/n
//
// 交换律逐位成立：mean 写成两个对称乘积之和，IEEE 754 加法可交换，
// 交换后两个加数逐位相同；delta 交换后只差一个符号，而取负在 IEEE 754
// 中是精确运算，故 delta^2 逐位相同；na*nb 同理。因此 Merge(a, b) 与
// Merge(b, a) 的结果逐位一致。
//
// 注意两点会破坏逐位交换律的浮点细节，这里都已规避：
//  1. Go 允许编译器把同一表达式内的乘加融合成 FMA（arm64 上确实会），
//     融合结果与运算顺序相关；用显式 float64 转换强制每个中间积
//     先舍入到 float64，阻断融合。
//  2. 浮点乘法可交换但不可结合：(d^2*na)*nb 与 (d^2*nb)*na 未必逐位
//     相同；先把 na*nb 算成单一乘积（乘法可交换，逐位相同），再与
//     d^2 相乘，保证交换后每一步的操作数都逐位一致。
//
// 空累加器是恒等元：与空累加器合并返回另一方的逐位拷贝；
// 两个空累加器合并仍为空。
func Merge(a, b *Accumulator) *Accumulator {
	sa := a.snapshot()
	sb := b.snapshot()

	out := &Accumulator{
		skipped: sa.skipped + sb.skipped,
		broken:  sa.broken || sb.broken,
	}

	switch {
	case sa.count == 0 && sb.count == 0:
		return out
	case sa.count == 0:
		out.count, out.mean, out.m2 = sb.count, sb.mean, sb.m2
		return out
	case sb.count == 0:
		out.count, out.mean, out.m2 = sa.count, sa.mean, sa.m2
		return out
	}

	n := sa.count + sb.count
	na := float64(sa.count)
	nb := float64(sb.count)
	nf := float64(n)

	delta := sb.mean - sa.mean
	out.count = n
	termA := float64(sa.mean * (na / nf))
	termB := float64(sb.mean * (nb / nf))
	out.mean = termA + termB
	deltaSq := float64(delta * delta)
	countProd := float64(na * nb)
	corr := float64(deltaSq * countProd / nf)
	m2sum := sa.m2 + sb.m2
	out.m2 = m2sum + corr
	if out.m2 < 0 {
		out.m2 = 0
	}
	return out
}
