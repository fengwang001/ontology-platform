// Package hst computes bucket assignment for an equal-width histogram.
// It depends on nothing; boundary semantics live here and here only.
package hst

// Bucket returns the index of the bucket owning v.
//
// Value range [0, maxValue) is split into maxValue/w buckets of width w:
// bucket k owns [k*w, (k+1)*w). A v exactly equal to k*w belongs to bucket
// k, not k-1. Every v >= maxValue (including v == maxValue) maps to the
// overflow bucket, whose index is maxValue/w — one past the last regular
// bucket. ok is false when v is negative (illegal) or the parameters are
// invalid (w <= 0, maxValue <= 0, or maxValue not a multiple of w).
func Bucket(v, w, maxValue int64) (idx int64, ok bool) {
	if w <= 0 || maxValue <= 0 || maxValue%w != 0 || v < 0 {
		return 0, false
	}
	if v >= maxValue {
		return maxValue / w, true // overflow bucket
	}
	return v / w, true
}

// Overflow returns the index of the overflow bucket for the given
// parameters, or ok=false when the parameters are invalid.
func Overflow(w, maxValue int64) (idx int64, ok bool) {
	if w <= 0 || maxValue <= 0 || maxValue%w != 0 {
		return 0, false
	}
	return maxValue / w, true
}
