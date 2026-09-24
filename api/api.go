// Package api is the public face of the incremental equal-width histogram.
// It depends on hagg; bucket assignment lives in hst under hagg.
package api

import (
	"errors"
	"fmt"
	"maps"

	"ontology/hagg"
	"ontology/hst"
)

// Distinguishable sentinel errors for every rejection kind.
var (
	ErrInvalidParam   = hagg.ErrInvalidParam
	ErrNegativeValue  = hagg.ErrNegativeValue
	ErrBucketAbsent   = hagg.ErrBucketAbsent
	ErrTooManyBuckets = hagg.ErrTooManyBuckets
)

// Histogram is a concurrent-safe incremental equal-width histogram.
type Histogram struct{ h *hagg.Hist }

// New validates W, maxValue and maxBuckets and returns an empty histogram.
func New(w, maxValue int64, maxBuckets int) (*Histogram, error) {
	h, err := hagg.New(w, maxValue, maxBuckets)
	if err != nil {
		return nil, err
	}
	return &Histogram{h: h}, nil
}

// Add records one occurrence of v.
func (g *Histogram) Add(v int64) error { return g.h.Add(v) }

// Remove withdraws one occurrence of v.
func (g *Histogram) Remove(v int64) error { return g.h.Remove(v) }

// Count returns the bucket count; ok is false when the bucket does not exist.
func (g *Histogram) Count(bucket int64) (int64, bool) { return g.h.Count(bucket) }

// Buckets returns a copy of the active bucket set.
func (g *Histogram) Buckets() map[int64]int64 { return g.h.Buckets() }

// SelfCheck verifies the four invariants on built-in Add/Remove sequences.
func SelfCheck() error {
	for _, check := range []func() error{
		checkEightSteps, checkBatchEquivalence, checkDisappearAndOverflow, checkFailures,
	} {
		if err := check(); err != nil {
			return err
		}
	}
	return nil
}

// The eight-step sequence from the spec (W=10, maxValue=100); x<0 means Remove(-x).
func checkEightSteps() error {
	h, err := New(10, 100, 64)
	if err != nil {
		return err
	}
	for _, x := range []int64{15, 25, 10, 100, 105, -25, -10, 20} {
		if x >= 0 {
			err = h.Add(x)
		} else {
			err = h.Remove(-x)
		}
		if err != nil {
			return fmt.Errorf("selfcheck eight-steps: %w", err)
		}
	}
	if want := map[int64]int64{1: 1, 2: 1, 10: 2}; !maps.Equal(h.Buckets(), want) {
		return fmt.Errorf("selfcheck eight-steps: got %v want %v", h.Buckets(), want)
	}
	return nil
}

// Invariant 1: incremental result equals batch recomputation of accepted ops.
func checkBatchEquivalence() error {
	h, err := New(7, 70, 128)
	if err != nil {
		return err
	}
	net, rng := map[int64]int64{}, int64(12345) // deterministic LCG, no deps
	for i := 0; i < 500; i++ {
		rng = (rng*6364136223846793005 + 1442695040888963407) >> 8
		v := rng % 80 // 70..79 land in the overflow bucket
		if i%3 == 0 {
			if h.Remove(v) == nil {
				net[v]--
			}
		} else if h.Add(v) == nil {
			net[v]++
		}
	}
	want := map[int64]int64{}
	for v, n := range net {
		if b, _ := hst.Bucket(v, 7, 70); n > 0 {
			want[b] += n
		}
	}
	if !maps.Equal(h.Buckets(), want) {
		return fmt.Errorf("selfcheck batch-equivalence: got %v want %v", h.Buckets(), want)
	}
	return nil
}

// Invariants 2 and 3: zero-count buckets vanish; overflow add/remove works.
func checkDisappearAndOverflow() error {
	h, _ := New(10, 100, 64)
	if h.Add(5) != nil || h.Remove(5) != nil {
		return errors.New("selfcheck: add/remove round trip failed")
	}
	if _, ok := h.Count(0); ok || len(h.Buckets()) != 0 {
		return errors.New("selfcheck: zero-count bucket did not disappear")
	}
	if h.Add(100) != nil || h.Add(105) != nil || h.Remove(100) != nil {
		return errors.New("selfcheck: overflow add/remove failed")
	}
	if c, ok := h.Count(10); !ok || c != 1 {
		return errors.New("selfcheck: overflow count wrong after remove")
	}
	if h.Remove(105) != nil {
		return errors.New("selfcheck: overflow remove failed")
	}
	if _, ok := h.Count(10); ok {
		return errors.New("selfcheck: overflow bucket did not disappear")
	}
	return nil
}

// Invariant 4: every rejection kind is distinguishable and leaves no trace.
func checkFailures() error {
	all := []error{ErrInvalidParam, ErrNegativeValue, ErrBucketAbsent, ErrTooManyBuckets}
	for i, a := range all {
		for j, b := range all {
			if i != j && errors.Is(a, b) {
				return errors.New("selfcheck: sentinel errors not distinct")
			}
		}
	}
	for _, p := range [][3]int64{{0, 100, 10}, {10, 0, 10}, {10, 95, 10}, {10, 100, 0}} {
		if _, err := New(p[0], p[1], int(p[2])); !errors.Is(err, ErrInvalidParam) {
			return errors.New("selfcheck: invalid params not rejected")
		}
	}
	h, _ := New(10, 100, 1)
	if h.Add(5) != nil {
		return errors.New("selfcheck: setup add failed")
	}
	before := h.Buckets()
	for _, op := range []struct{ got, want error }{
		{h.Add(-1), ErrNegativeValue}, {h.Remove(-1), ErrNegativeValue},
		{h.Remove(50), ErrBucketAbsent}, {h.Add(15), ErrTooManyBuckets},
	} {
		if !errors.Is(op.got, op.want) || !maps.Equal(h.Buckets(), before) {
			return fmt.Errorf("selfcheck: rejection misbehaved: %v", op.got)
		}
	}
	if h.Add(5) != nil { // still usable after rejections
		return errors.New("selfcheck: histogram unusable after rejections")
	}
	return nil
}
