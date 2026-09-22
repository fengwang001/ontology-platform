package ontology

// Add 向直方图加入一个样本。
//
// NaN 会被拒绝：计入 skipped 并返回 ErrNaN，不进任何桶也不进溢出。
// -Inf 计入下溢（underflow），+Inf 计入上溢（overflow）。
// +0.0 与 -0.0 按数值 0 参与比较（x < lo / x >= hi 对两者行为一致）。
func (h *Histogram) Add(x float64) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.added++

	i := h.indexFor(x)
	switch {
	case i == idxNaN:
		h.skipped++
		return ErrNaN
	case i < 0:
		h.under++
	case i >= h.n:
		h.over++
	default:
		h.counts[i]++
	}
	return nil
}

// Buckets 返回各桶计数的拷贝，调用方修改返回切片不影响直方图内部状态。
func (h *Histogram) Buckets() []uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]uint64, len(h.counts))
	copy(out, h.counts)
	return out
}

// Underflow 返回落在 [lo, hi) 之下的样本数（含 -Inf，不含 NaN）。
func (h *Histogram) Underflow() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.under
}

// Overflow 返回落在 [lo, hi) 之上的样本数（含 hi 本身与 +Inf）。
func (h *Histogram) Overflow() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.over
}

// Skipped 返回被跳过的样本数（目前即 NaN 样本数）。
func (h *Histogram) Skipped() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.skipped
}

// Added 返回迄今为止 Add 的调用总次数（含被拒绝的 NaN）。
func (h *Histogram) Added() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.added
}
