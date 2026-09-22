package ontology

import "math"

func (h *Histogram) Add(x float64) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.added++
	if math.IsNaN(x) {
		h.skipped++
		return ErrNaNSample
	}

	if x < h.lo || math.IsInf(x, -1) {
		h.underflow++
		return nil
	}
	if x >= h.hi || math.IsInf(x, 1) {
		h.overflow++
		return nil
	}

	h.buckets[h.indexFor(x)]++
	return nil
}
