package ontology

import "math"

func bucketIndex(lo, hi, width float64, n int, x float64) int {
	i := int((x - lo) / width)
	if i < 0 {
		i = 0
	}
	if i >= n {
		i = n - 1
	}

	lower := lo + float64(i)*width
	upper := lo + float64(i+1)*width
	if i == n-1 {
		upper = hi
	}

	if i > 0 && x < lower {
		i--
	} else if i+1 < n && x >= upper {
		i++
	}

	return i
}

func (h *Histogram) indexFor(x float64) int {
	return bucketIndex(h.lo, h.hi, h.width, len(h.buckets), x)
}

func NaiveBucketIndex(lo, hi float64, n int, x float64) int {
	return int((x - lo) / ((hi - lo) / float64(n)))
}

func BucketIndex(lo, hi float64, n int, x float64) (int, error) {
	if math.IsNaN(lo) || math.IsNaN(hi) {
		return 0, ErrNaNBound
	}
	if math.IsInf(lo, 0) || math.IsInf(hi, 0) {
		return 0, ErrInfBound
	}
	if n <= 0 {
		return 0, ErrInvalidBucketCount
	}
	if lo >= hi {
		return 0, ErrInvalidRange
	}
	if math.IsNaN(x) {
		return 0, ErrNaNSample
	}

	width := (hi - lo) / float64(n)
	if x < lo {
		return -1, nil
	}
	if x >= hi {
		return n, nil
	}
	return bucketIndex(lo, hi, width, n, x), nil
}
