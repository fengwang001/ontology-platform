// Package histogram implements a concurrent-safe incremental histogram with
// equal-width buckets over a left-closed, right-open value range.
package histogram

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Sentinel errors. Callers can distinguish rejection reasons with errors.Is.
var (
	// ErrInvalidWidth is returned when the bucket width is not positive.
	ErrInvalidWidth = errors.New("histogram: bucket width must be positive")
	// ErrInvalidUpperBound is returned when the range upper bound is not
	// positive or is not an exact integer multiple of the bucket width.
	ErrInvalidUpperBound = errors.New("histogram: upper bound must be positive and an integer multiple of bucket width")
	// ErrInvalidMaxBuckets is returned when the active-bucket limit is not positive.
	ErrInvalidMaxBuckets = errors.New("histogram: max active buckets must be positive")
	// ErrNegativeValue is returned when an added or retracted value is negative.
	ErrNegativeValue = errors.New("histogram: value must not be negative")
	// ErrBucketNotFound is returned when querying or retracting a value whose
	// bucket currently has no observations (a zero-count bucket never exists).
	ErrBucketNotFound = errors.New("histogram: bucket not found")
	// ErrTooManyBuckets is returned when an operation would create a new
	// active bucket beyond the configured limit.
	ErrTooManyBuckets = errors.New("histogram: active bucket limit exceeded")
)

// Bucket describes one active (non-empty) bucket in the histogram.
type Bucket struct {
	// Index is the zero-based bucket index. Regular buckets are numbered
	// 0..NumBuckets-1; the overflow bucket index equals NumBuckets.
	Index int64
	// Lower is the inclusive lower bound of the bucket. For the overflow
	// bucket it equals the histogram upper bound.
	Lower int64
	// Upper is the exclusive upper bound of the bucket. For the overflow
	// bucket it is zero, meaning no finite upper bound.
	Upper int64
	// Count is the number of observations currently in the bucket; always > 0.
	Count int64
	// Overflow reports whether this is the overflow bucket for values at or
	// above the histogram upper bound.
	Overflow bool
}

// Histogram is an equal-width incremental histogram safe for concurrent use.
//
// Values fall in the half-open range [0, upperBound). Regular bucket i covers
// [i*width, (i+1)*width). Values >= upperBound land in a single overflow
// bucket. A bucket exists only while its count is positive: retracting the
// last observation removes the bucket, and later queries report
// ErrBucketNotFound rather than a zero-count bucket.
type Histogram struct {
	mu       sync.RWMutex
	width    int64
	upper    int64
	num      int64
	maxSlots int
	counts   map[int64]int64
}

// New creates a histogram with the given configuration.
//
// width must be positive; upperBound must be positive and an exact integer
// multiple of width; maxActiveBuckets must be positive and bounds the number
// of simultaneously non-empty buckets (including the overflow bucket).
func New(width, upperBound int64, maxActiveBuckets int) (*Histogram, error) {
	if width <= 0 {
		return nil, ErrInvalidWidth
	}
	if upperBound <= 0 || upperBound%width != 0 {
		return nil, ErrInvalidUpperBound
	}
	if maxActiveBuckets <= 0 {
		return nil, ErrInvalidMaxBuckets
	}
	return &Histogram{
		width:    width,
		upper:    upperBound,
		num:      upperBound / width,
		maxSlots: maxActiveBuckets,
		counts:   make(map[int64]int64),
	}, nil
}

// bucketIndex maps a non-negative value to its bucket index.
func (h *Histogram) bucketIndex(value int64) int64 {
	if value >= h.upper {
		return h.num
	}
	return value / h.width
}

// bucketLocked builds the Bucket description for an active index.
func (h *Histogram) bucketLocked(index int64) Bucket {
	if index >= h.num {
		return Bucket{
			Index:    index,
			Lower:    h.upper,
			Upper:    0,
			Count:    h.counts[index],
			Overflow: true,
		}
	}
	return Bucket{
		Index:    index,
		Lower:    index * h.width,
		Upper:    (index + 1) * h.width,
		Count:    h.counts[index],
		Overflow: false,
	}
}

// Add records one observation of value. Negative values are rejected. Adding
// to a previously empty bucket is rejected (without changing anything) if it
// would exceed the active-bucket limit.
func (h *Histogram) Add(value int64) error {
	if value < 0 {
		return fmt.Errorf("%w: %d", ErrNegativeValue, value)
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	index := h.bucketIndex(value)
	if _, exists := h.counts[index]; !exists && len(h.counts) >= h.maxSlots {
		return fmt.Errorf("%w: value %d would open bucket %d", ErrTooManyBuckets, value, index)
	}
	h.counts[index]++
	return nil
}

// Retract removes one previously recorded observation of value. Negative
// values and values whose bucket currently does not exist are rejected. When
// the remaining count reaches zero the bucket is removed from the histogram.
func (h *Histogram) Retract(value int64) error {
	if value < 0 {
		return fmt.Errorf("%w: %d", ErrNegativeValue, value)
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	index := h.bucketIndex(value)
	count, exists := h.counts[index]
	if !exists {
		return fmt.Errorf("%w: value %d belongs to bucket %d which is empty", ErrBucketNotFound, value, index)
	}
	if count == 1 {
		delete(h.counts, index)
		return nil
	}
	h.counts[index] = count - 1
	return nil
}

// BucketOf returns the active bucket containing value. It returns
// ErrBucketNotFound when the bucket exists but has no observations, and
// ErrNegativeValue when value is negative.
func (h *Histogram) BucketOf(value int64) (Bucket, error) {
	if value < 0 {
		return Bucket{}, fmt.Errorf("%w: %d", ErrNegativeValue, value)
	}
	h.mu.RLock()
	defer h.mu.RUnlock()

	index := h.bucketIndex(value)
	if _, exists := h.counts[index]; !exists {
		return Bucket{}, fmt.Errorf("%w: value %d belongs to bucket %d which is empty", ErrBucketNotFound, value, index)
	}
	return h.bucketLocked(index), nil
}

// Buckets returns a point-in-time snapshot of all active buckets ordered by
// ascending bucket index. Every bucket in the snapshot has a positive count.
func (h *Histogram) Buckets() []Bucket {
	h.mu.RLock()
	defer h.mu.RUnlock()

	active := make([]int64, 0, len(h.counts))
	for index := range h.counts {
		active = append(active, index)
	}
	sort.Slice(active, func(i, j int) bool { return active[i] < active[j] })

	buckets := make([]Bucket, len(active))
	for i, index := range active {
		buckets[i] = h.bucketLocked(index)
	}
	return buckets
}

// NumBuckets returns the number of regular (non-overflow) buckets.
func (h *Histogram) NumBuckets() int64 { return h.num }

// Width returns the configured bucket width.
func (h *Histogram) Width() int64 { return h.width }

// UpperBound returns the configured value-range upper bound.
func (h *Histogram) UpperBound() int64 { return h.upper }

// ActiveBuckets returns the current number of active (non-empty) buckets.
func (h *Histogram) ActiveBuckets() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.counts)
}
