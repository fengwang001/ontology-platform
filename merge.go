package ontology

import "math"

// Merge 把两个独立累加器合并为一个新的累加器，不修改 a 或 b。
//
// 合并公式（Chan et al.，Welford 递推的并行推广）：
//
//	n    = nA + nB
//	delta = meanB - meanA
//	mean = meanA + delta*nB/n
//	M2   = M2A + M2B + delta^2*nA*nB/n
//
// 该公式本身对操作数顺序不是逐位对称的，因此 Merge 在计算前
// 先按 (n, mean 的位模式, M2 的位模式) 对两个操作数做确定性排序，
// 保证 Merge(a, b) 与 Merge(b, a) 走完全相同的浮点运算路径，
// 结果逐位相同。
//
// 空累加器是恒等元：Merge(a, 空) 的结果与 a 逐位相同，
// 两个空累加器合并仍为空。任一方已被 ±Inf 污染时结果同样被污染。
// 跳过计数取两者之和。
func Merge(a, b *Accumulator) *Accumulator {
	sa := lockSnapshot(a)
	sb := lockSnapshot(b)

	if sa.n == 0 && sb.n == 0 {
		return &Accumulator{skipped: sa.skipped + sb.skipped}
	}
	if sa.n == 0 {
		return &Accumulator{n: sb.n, mean: sb.mean, m2: sb.m2, skipped: sa.skipped + sb.skipped, poisoned: sb.poisoned}
	}
	if sb.n == 0 {
		return &Accumulator{n: sa.n, mean: sa.mean, m2: sa.m2, skipped: sa.skipped + sb.skipped, poisoned: sa.poisoned}
	}

	if lessSnap(sb, sa) {
		sa, sb = sb, sa
	}

	n := sa.n + sb.n
	nf := float64(n)
	delta := sb.mean - sa.mean
	mean := sa.mean + delta*float64(sb.n)/nf
	m2 := sa.m2 + sb.m2 + delta*delta*float64(sa.n)*float64(sb.n)/nf

	return &Accumulator{
		n:        n,
		mean:     mean,
		m2:       m2,
		skipped:  sa.skipped + sb.skipped,
		poisoned: sa.poisoned || sb.poisoned,
	}
}

// snap 是累加器内部状态的一致性快照。
type snap struct {
	n        int64
	skipped  int64
	mean     float64
	m2       float64
	poisoned bool
}

// lockSnapshot 在持锁期间拷贝状态，保证读到的是一致快照而非半更新状态。
func lockSnapshot(a *Accumulator) snap {
	a.mu.Lock()
	defer a.mu.Unlock()
	n, skipped, mean, m2, poisoned := a.snapshot()
	return snap{n: n, skipped: skipped, mean: mean, m2: m2, poisoned: poisoned}
}

// lessSnap 定义操作数的确定性全序，使 Merge 满足逐位交换律。
func lessSnap(x, y snap) bool {
	if x.n != y.n {
		return x.n < y.n
	}
	if bx, by := math.Float64bits(x.mean), math.Float64bits(y.mean); bx != by {
		return bx < by
	}
	return math.Float64bits(x.m2) < math.Float64bits(y.m2)
}
