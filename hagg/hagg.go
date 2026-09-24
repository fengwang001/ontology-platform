// Package hagg maintains per-bucket counts with Add/Remove and
// bucket-disappearance semantics. It depends on hst for bucket assignment.
package hagg

import (
	"errors"
	"sync"

	"ontology/hst"
)

// Sentinel errors, each kind of rejection is distinguishable via errors.Is.
var (
	ErrInvalidParam   = errors.New("hagg: invalid parameters")
	ErrNegativeValue  = errors.New("hagg: negative value")
	ErrBucketAbsent   = errors.New("hagg: bucket absent")
	ErrTooManyBuckets = errors.New("hagg: too many buckets")
)

// Hist is an incremental equal-width histogram over [0, maxValue) plus one
// overflow bucket. Safe for concurrent use.
type Hist struct {
	mu         sync.RWMutex
	w, maxV    int64
	maxBuckets int
	counts     map[int64]int64
	lastProbes int // buckets inspected to locate the target in the last Add/Remove
}

// New validates parameters and returns an empty histogram.
func New(w, maxValue int64, maxBuckets int) (*Hist, error) {
	if _, ok := hst.Overflow(w, maxValue); !ok || maxBuckets <= 0 {
		return nil, ErrInvalidParam
	}
	return &Hist{w: w, maxV: maxValue, maxBuckets: maxBuckets, counts: map[int64]int64{}}, nil
}

// Add increments the bucket owning v. Rejections leave the state untouched.
func (h *Hist) Add(v int64) error {
	b, ok := hst.Bucket(v, h.w, h.maxV)
	if !ok {
		return ErrNegativeValue // params were validated in New, so v < 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lastProbes = 1 // hash lookup: only the target bucket is inspected
	if _, exists := h.counts[b]; !exists && len(h.counts) >= h.maxBuckets {
		return ErrTooManyBuckets
	}
	h.counts[b]++
	return nil
}

// Remove decrements the bucket owning v; a bucket reaching zero disappears.
func (h *Hist) Remove(v int64) error {
	b, ok := hst.Bucket(v, h.w, h.maxV)
	if !ok {
		return ErrNegativeValue
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lastProbes = 1
	c, exists := h.counts[b]
	if !exists {
		return ErrBucketAbsent
	}
	if c == 1 {
		delete(h.counts, b) // invariant 2: zero-count buckets vanish at once
	} else {
		h.counts[b] = c - 1
	}
	return nil
}

// Count returns the count of bucket and whether the bucket exists.
func (h *Hist) Count(bucket int64) (int64, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	c, ok := h.counts[bucket]
	return c, ok
}

// Buckets returns a copy of the active bucket set.
func (h *Hist) Buckets() map[int64]int64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make(map[int64]int64, len(h.counts))
	for k, v := range h.counts {
		out[k] = v
	}
	return out
}

// CheckConstantTimeLocate reports whether bucket location stays a
// constant-time hash lookup as the number of active buckets grows. It
// exposes only a verdict; the probe counter itself stays unexported.
func CheckConstantTimeLocate() bool {
	for _, m := range []int{100, 1000, 10000} {
		h, err := New(1, int64(m), m)
		if err != nil {
			return false
		}
		for i := 0; i < m; i++ {
			if h.Add(int64(i)) != nil {
				return false
			}
		}
		if h.Add(0) != nil || h.lastProbes > 2 {
			return false
		}
	}
	return true
}
