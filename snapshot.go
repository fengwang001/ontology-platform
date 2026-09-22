package ontology

type Snapshot struct {
	Lo        float64
	Hi        float64
	Buckets   []uint64
	Underflow uint64
	Overflow  uint64
	Skipped   uint64
	Added     uint64
}

func (h *Histogram) Params() (lo, hi float64, n int) {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.lo, h.hi, len(h.buckets)
}

func (h *Histogram) Buckets() []uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()

	result := make([]uint64, len(h.buckets))
	copy(result, h.buckets)
	return result
}

func (h *Histogram) Underflow() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.underflow
}

func (h *Histogram) Overflow() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.overflow
}

func (h *Histogram) Skipped() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.skipped
}

func (h *Histogram) Added() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.added
}

func (h *Histogram) Snapshot() Snapshot {
	h.mu.Lock()
	defer h.mu.Unlock()

	buckets := make([]uint64, len(h.buckets))
	copy(buckets, h.buckets)
	return Snapshot{
		Lo:        h.lo,
		Hi:        h.hi,
		Buckets:   buckets,
		Underflow: h.underflow,
		Overflow:  h.overflow,
		Skipped:   h.skipped,
		Added:     h.added,
	}
}
