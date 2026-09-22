package ontology

import "math"

func New(lo, hi float64, n int) (*Histogram, error) {
	if math.IsNaN(lo) || math.IsNaN(hi) {
		return nil, ErrNaNBound
	}
	if math.IsInf(lo, 0) || math.IsInf(hi, 0) {
		return nil, ErrInfBound
	}
	if n <= 0 {
		return nil, ErrInvalidBucketCount
	}
	if lo >= hi {
		return nil, ErrInvalidRange
	}

	return &Histogram{
		lo:      lo,
		hi:      hi,
		width:   (hi - lo) / float64(n),
		buckets: make([]uint64, n),
	}, nil
}
