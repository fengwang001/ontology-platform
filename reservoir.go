package ontology

import (
	"container/heap"
	"fmt"
	"math"
	"sort"
	"sync"
)

// Reservoir is a fixed-capacity weighted reservoir sampler.
//
// Algorithm: A-Res (Efraimidis & Spirakis, "Weighted random sampling
// with a reservoir", 2005). Each arriving element with weight w draws
// u ~ Uniform(0,1] and is assigned key u^(1/w); the k elements with the
// largest keys are kept in a min-heap. This yields P(selected) ~ w
// exactly, processes the stream in one pass, and never stores more than
// k elements, so memory is O(k) regardless of stream length.
//
// All randomness is drawn inside Add. Sample only reads the heap, so
// repeated Sample calls without intervening Adds return identical
// results. The zero value is not usable; construct with New.
type Reservoir[T any] struct {
	mu        sync.Mutex
	k         int
	rng       *splitmix64
	randCount uint64
	heap      entryHeap[T]
	seq       uint64
	added     uint64
	rejected  uint64
}

// New returns a Reservoir of capacity k seeded with seed.
// It returns ErrNonPositiveCapacity when k <= 0.
func New[T any](k int, seed uint64) (*Reservoir[T], error) {
	if k <= 0 {
		return nil, fmt.Errorf("%w: got k=%d", ErrNonPositiveCapacity, k)
	}
	return &Reservoir[T]{k: k, rng: newRNG(seed)}, nil
}

// Add offers one element with the given weight to the reservoir.
//
// The weight must be a positive integer; zero, negative, non-integer,
// NaN and Inf weights are rejected with ErrInvalidWeight and the element
// is counted via Rejected without entering the reservoir or consuming
// randomness. Each accepted element consumes exactly one random number,
// so RandCount equals Added at all times.
func (r *Reservoir[T]) Add(value T, weight float64) error {
	if !validWeight(weight) {
		r.mu.Lock()
		r.rejected++
		r.mu.Unlock()
		return fmt.Errorf("%w: got %v", ErrInvalidWeight, weight)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := math.Pow(r.rng.uniform(), 1.0/weight)
	r.randCount++
	e := entry[T]{value: value, key: key, seq: r.seq}
	r.seq++
	r.added++
	if r.heap.Len() < r.k {
		heap.Push(&r.heap, e)
		return nil
	}
	if key > r.heap[0].key {
		r.heap[0] = e
		heap.Fix(&r.heap, 0)
	}
	return nil
}

// Sample returns the current reservoir contents in arrival order.
//
// The returned slice is a copy; mutating it does not affect the
// reservoir. Sample draws no randomness, so repeated calls without
// intervening Add calls return identical results.
func (r *Reservoir[T]) Sample() []T {
	r.mu.Lock()
	defer r.mu.Unlock()
	entries := make([]entry[T], len(r.heap))
	copy(entries, r.heap)
	sort.Slice(entries, func(i, j int) bool { return entries[i].seq < entries[j].seq })
	out := make([]T, len(entries))
	for i, e := range entries {
		out[i] = e.value
	}
	return out
}

// Size returns the number of elements currently held (never exceeds k).
func (r *Reservoir[T]) Size() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.heap.Len()
}

// Capacity returns k.
func (r *Reservoir[T]) Capacity() int { return r.k }

// Added returns the number of elements accepted via Add.
func (r *Reservoir[T]) Added() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.added
}

// Rejected returns the number of elements refused for invalid weight.
func (r *Reservoir[T]) Rejected() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rejected
}

// RandCount returns how many random numbers this sampler has consumed.
func (r *Reservoir[T]) RandCount() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.randCount
}

// validWeight reports whether w is a positive integer representable as int64.
func validWeight(w float64) bool {
	return !math.IsNaN(w) && !math.IsInf(w, 0) &&
		w >= 1 && w == math.Trunc(w) && w <= math.MaxInt64
}
