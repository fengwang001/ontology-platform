package ontology

import "fmt"

// Snapshot 是直方图某一时刻的只读状态副本。
type Snapshot struct {
	Lo, Hi float64
	N      int
	Counts []uint64
	Under  uint64
	Over   uint64
	Skip   uint64
	Added  uint64
}

// Snapshot 取出当前状态的完整拷贝。
func (h *Histogram) Snapshot() Snapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	counts := make([]uint64, len(h.counts))
	copy(counts, h.counts)
	return Snapshot{
		Lo: h.lo, Hi: h.hi, N: h.n,
		Counts: counts,
		Under:  h.under, Over: h.over,
		Skip: h.skipped, Added: h.added,
	}
}

// Merge 返回两个同参数直方图合并后的新直方图：逐桶计数相加，
// 下溢、上溢、跳过数与 Add 次数也相加。
//
// 双方 (lo, hi, n) 必须完全相同，否则返回包有 ErrMismatchedSpec 的错误，
// 且错误信息指出两边的参数。Merge 不修改任何一个输入直方图。
func (h *Histogram) Merge(other *Histogram) (*Histogram, error) {
	if other == nil {
		return nil, fmt.Errorf("%w: other histogram is nil", ErrMismatchedSpec)
	}

	left := h.Snapshot()
	right := other.Snapshot()

	if left.Lo != right.Lo || left.Hi != right.Hi || left.N != right.N {
		return nil, fmt.Errorf("%w: left=(%v,%v,%d), right=(%v,%v,%d)",
			ErrMismatchedSpec, left.Lo, left.Hi, left.N, right.Lo, right.Hi, right.N)
	}

	out := &Histogram{
		lo:     left.Lo,
		hi:     left.Hi,
		n:      left.N,
		w:      (left.Hi - left.Lo) / float64(left.N),
		counts: make([]uint64, left.N),
	}
	for i := range out.counts {
		out.counts[i] = left.Counts[i] + right.Counts[i]
	}
	out.under = left.Under + right.Under
	out.over = left.Over + right.Over
	out.skipped = left.Skip + right.Skip
	out.added = left.Added + right.Added
	return out, nil
}

// IdentityHolds 报告计数恒等式是否成立：
// 所有桶计数之和 + 下溢 + 上溢 + 跳过数 == Add 次数。
func (s Snapshot) IdentityHolds() bool {
	var sum uint64
	for _, c := range s.Counts {
		sum += c
	}
	return sum+s.Under+s.Over+s.Skip == s.Added
}
