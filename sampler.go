// Package ontology implements a reproducible weighted reservoir sampler.
//
// The sampler draws a fixed-capacity sample of k elements from a stream
// of unknown length, where each element carries a positive integer
// weight and heavier elements are more likely to be retained.
//
// Algorithm: A-Res (Efraimidis & Spirakis, "Weighted random sampling
// with a reservoir", 2006). Each arriving element with weight w is
// assigned a key u^(1/w) where u is drawn from Uniform(0,1). The k
// elements with the largest keys form the sample. Keys are kept in a
// min-heap of capacity k, so memory is O(k) regardless of stream
// length: the stream itself is never buffered. Each Add costs one
// random draw and O(log k) heap work.
//
// Reproducibility: all randomness comes from a PCG generator seeded
// with an explicit uint64 seed at construction. No global rand, no
// clock, no map iteration. Randomness is consumed only inside Add;
// Sample is a pure read of the current reservoir, so repeated Sample
// calls without intervening Adds return identical results.
//
// Concurrency: Add and Sample are safe for concurrent use; a single
// mutex serializes all state access.
package ontology

import (
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"sync"
)

// ErrInvalidCapacity is returned by New when k <= 0.
var ErrInvalidCapacity = errors.New("ontology: reservoir capacity must be a positive integer")

// ErrInvalidWeight is returned by Add when the weight is not a positive
// integer (zero, negative, non-integer, NaN or Inf). The element is
// rejected and never enters the reservoir.
var ErrInvalidWeight = errors.New("ontology: weight must be a positive integer")

// Reservoir is a fixed-capacity weighted reservoir sampler.
type Reservoir struct {
	mu       sync.Mutex
	k        int
	rng      *rand.Rand
	consumed uint64
	heap     minHeap
	accepted uint64
	rejected uint64
}

// New creates a Reservoir of capacity k seeded with seed. It returns
// ErrInvalidCapacity if k <= 0.
func New(k int, seed uint64) (*Reservoir, error) {
	if k <= 0 {
		return nil, fmt.Errorf("%w: got %d", ErrInvalidCapacity, k)
	}
	return &Reservoir{
		k:    k,
		rng:  rand.New(rand.NewPCG(seed, seed^0x9E3779B97F4A7C15)),
		heap: make(minHeap, 0, k),
	}, nil
}

// Add offers one element with the given weight to the reservoir. The
// weight must be a positive integer; anything else (zero, negative,
// fractional, NaN, Inf) is rejected with ErrInvalidWeight and the
// element does not enter the reservoir. Exactly one random number is
// consumed per accepted element.
func (r *Reservoir) Add(value string, weight float64) error {
	w, err := checkWeight(weight)
	if err != nil {
		r.mu.Lock()
		r.rejected++
		r.mu.Unlock()
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	u := r.rng.Float64()
	r.consumed++
	r.accepted++
	e := entry{value: value, weight: w, key: math.Pow(u, 1.0/float64(w))}
	switch {
	case len(r.heap) < r.k:
		r.heap.push(e)
	case e.key > r.heap.peek().key:
		r.heap.replaceTop(e)
	}
	return nil
}

// Sample returns a snapshot of the current reservoir contents, ordered
// by key descending (most retained first). The result is a fresh slice
// that shares no state with the reservoir; mutating it has no effect
// on the sampler. Sample consumes no randomness: without intervening
// Add calls, repeated Sample calls return identical results.
func (r *Reservoir) Sample() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	entries := make([]entry, len(r.heap))
	copy(entries, r.heap)
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].key > entries[j].key
	})
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.value
	}
	return out
}

// Len reports the current number of elements held in the reservoir.
// It never exceeds the capacity k.
func (r *Reservoir) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.heap)
}

// Total reports how many elements were accepted (valid weight).
func (r *Reservoir) Total() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.accepted
}

// Rejected reports how many elements were rejected for invalid weight.
func (r *Reservoir) Rejected() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rejected
}

// RandConsumed reports how many random numbers this sampler has drawn
// since construction. Two runs with the same seed and input sequence
// consume the same count.
func (r *Reservoir) RandConsumed() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.consumed
}

// checkWeight validates that weight is a positive integer and returns
// it as an int.
func checkWeight(weight float64) (int, error) {
	if math.IsNaN(weight) || math.IsInf(weight, 0) ||
		weight < 1 || weight != math.Trunc(weight) ||
		weight > float64(math.MaxInt32) {
		return 0, fmt.Errorf("%w: got %v", ErrInvalidWeight, weight)
	}
	return int(weight), nil
}
