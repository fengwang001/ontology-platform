package ontology

import "fmt"

func (h *Histogram) Merge(other *Histogram) (*Histogram, error) {
	h.mu.Lock()
	if other == nil {
		h.mu.Unlock()
		return nil, fmt.Errorf("%w: source is nil", ErrHistogramMismatch)
	}
	if other == h {
		h.mu.Unlock()
		return nil, fmt.Errorf("%w: cannot merge a histogram with itself", ErrHistogramMismatch)
	}

	other.mu.Lock()
	defer other.mu.Unlock()
	defer h.mu.Unlock()

	if h.lo != other.lo || h.hi != other.hi || len(h.buckets) != len(other.buckets) {
		return nil, fmt.Errorf(
			"%w: target=(%v,%v,%d) source=(%v,%v,%d)",
			ErrHistogramMismatch,
			h.lo, h.hi, len(h.buckets),
			other.lo, other.hi, len(other.buckets),
		)
	}

	result := &Histogram{
		lo:        h.lo,
		hi:        h.hi,
		width:     h.width,
		buckets:   make([]uint64, len(h.buckets)),
		underflow: h.underflow + other.underflow,
		overflow:  h.overflow + other.overflow,
		skipped:   h.skipped + other.skipped,
		added:     h.added + other.added,
	}
	for i := range h.buckets {
		result.buckets[i] = h.buckets[i] + other.buckets[i]
	}

	return result, nil
}
