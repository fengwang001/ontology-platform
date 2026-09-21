package stats

import (
	"math"
	"sync"
)

// Accumulator is an online accumulator of float64 samples.
//
// It tracks the count, mean and central second moment with Welford's
// one-pass recurrence, which avoids the catastrophic cancellation of the
// naive sum-of-squares formula (sum(x^2) - n*mean^2).
//
// All methods are safe for concurrent use. The internal state is guarded
// by a mutex so readers never observe a partially applied update.
type Accumulator struct {
	// mu guards every field below. It is held for O(1) work only.
	mu sync.Mutex

	// n is the number of finite samples that have been accepted.
	n uint64

	// mean is the running arithmetic mean (Welford M1).
	mean float64

	// m2 is the running sum of squared deviations from the running mean
	// (Welford M2): m2 = sum_i (x_i - mean_at_step_i) * (x_i - mean_now).
	// It is always >= 0 for finite inputs and is used directly, without
	// subtracting large nearly-equal quantities.
	m2 float64

	// skipped counts NaN samples that Add rejected. NaNs never touch n,
	// mean or m2, so they cannot poison the statistics.
	skipped uint64

	// invalid records that an infinite sample made the statistics
	// non-finite. Once set, Mean/Variance/SampleVariance return
	// ErrStatsUnavailable.
	invalid bool
}

// New returns an empty accumulator.
func New() *Accumulator {
	return &Accumulator{}
}

// Count returns the number of finite samples accepted so far.
func (a *Accumulator) Count() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.n
}

// Skipped returns the number of NaN samples rejected by Add.
func (a *Accumulator) Skipped() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.skipped
}

// Valid reports whether finite statistics are still available.
func (a *Accumulator) Valid() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return !a.invalid
}

// Add incorporates one sample. NaN is rejected and counted as skipped.
//
// Welford's recurrence (one pass, no sum-of-squares subtraction):
//
//	n1    = n + 1
//	delta = x - mean
//	mean1 = mean + delta/n1
//	m2_1  = m2 + delta*(x - mean1)
//
// Each update is a single critical section, so readers can never see
// count advanced while mean/m2 still lag behind.
func (a *Accumulator) Add(x float64) {
	// NaN is the only value that is rejected outright: it can neither be
	// compared nor folded into any meaningful statistic. +0.0 and -0.0
	// are equal to 0 and flow through the normal recurrence.
	if math.IsNaN(x) {
		a.mu.Lock()
		a.skipped++
		a.mu.Unlock()
		return
	}

	a.lockState(func() {
		// Infinity participates in the recurrence; any non-finite mean
		// or m2 flags the statistics as unusable.
		a.addFinite(x)
		if math.IsNaN(a.mean) || math.IsInf(a.mean, 0) ||
			math.IsNaN(a.m2) || math.IsInf(a.m2, 0) {
			a.invalid = true
		}
		a.n++
	})
}

// Mean returns the arithmetic mean, or a sentinel error when undefined.
func (a *Accumulator) Mean() (float64, error) {
	s := a.state()
	if s.n == 0 {
		return math.NaN(), ErrNoSamples
	}
	if s.invalid || math.IsNaN(s.mean) || math.IsInf(s.mean, 0) {
		return math.NaN(), ErrStatsUnavailable
	}
	return s.mean, nil
}

// Variance returns the population variance (m2/n).
func (a *Accumulator) Variance() (float64, error) {
	s := a.state()
	if s.n == 0 {
		return math.NaN(), ErrNoSamples
	}
	if s.invalid {
		return math.NaN(), ErrStatsUnavailable
	}
	// m2 is a sum of squared deviations computed incrementally; floating
	// point roundoff can in principle push it a hair below zero, and a
	// variance must never be reported as negative.
	v := s.m2 / float64(s.n)
	if v < 0 {
		return 0, nil
	}
	return v, nil
}

// SampleVariance returns the unbiased sample variance (m2/(n-1)).
func (a *Accumulator) SampleVariance() (float64, error) {
	s := a.state()
	if s.n == 0 {
		return math.NaN(), ErrNoSamples
	}
	if s.n == 1 {
		return math.NaN(), ErrSingleSample
	}
	if s.invalid {
		return math.NaN(), ErrStatsUnavailable
	}
	v := s.m2 / (float64(s.n) - 1)
	if v < 0 {
		return 0, nil
	}
	return v, nil
}

// addFinite performs one Welford step. The caller must hold a.mu.
func (a *Accumulator) addFinite(x float64) {
	n1 := float64(a.n + 1)
	delta := x - a.mean
	mean1 := a.mean + delta/n1
	a.m2 += delta * (x - mean1)
	a.mean = mean1
}
