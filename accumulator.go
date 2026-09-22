// Package ontology provides an online statistics accumulator for
// streaming float64 samples. It computes count, mean, population
// variance and sample variance in a numerically stable way and
// supports order-independent merging of independent accumulators.
//
// Numerical method: Welford's online recurrence, with the parallel
// combination formula of Chan, Golub & LeVeque (1979) for Merge.
// For each accepted sample x with running count n, mean and M2
// (the sum of squared deviations from the running mean):
//
//	delta  = x - mean
//	mean  += delta / n
//	M2    += delta * (x - mean)
//
//	population variance = M2 / n
//	sample variance     = M2 / (n - 1)
//
// M2 is mathematically non-negative; identical samples keep it at
// exactly 0, so variance is never negative and never a spurious
// tiny negative float.
package ontology

import (
	"math"
	"sync"
)

// Accumulator holds running statistics for a stream of float64
// samples. The zero value is ready to use. It is safe for
// concurrent use by multiple goroutines.
type Accumulator struct {
	mu       sync.Mutex
	count    int64
	mean     float64
	m2       float64
	skipped  int64
	poisoned bool
}

// New returns an empty Accumulator.
func New() *Accumulator { return &Accumulator{} }

// Add feeds one sample into the accumulator.
//
// NaN samples are rejected: Add returns ErrNaNSample, the sample is
// not counted and the running statistics are left untouched; the
// rejection is recorded in Skipped.
//
// +Inf/-Inf samples are accepted and counted, but they make the
// statistics unrepresentable: subsequent Mean, Variance and
// SampleVariance calls return ErrStatsUnavailable.
//
// +0 and -0 are both valid samples and are treated as numeric zero.
func (a *Accumulator) Add(x float64) error {
	if x != x { // NaN
		a.mu.Lock()
		a.skipped++
		a.mu.Unlock()
		return ErrNaNSample
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.count++
	if math.IsInf(x, 0) {
		a.poisoned = true
		a.mean = math.NaN()
		a.m2 = math.NaN()
		return nil
	}
	if a.poisoned {
		return nil
	}
	delta := x - a.mean
	a.mean += delta / float64(a.count)
	a.m2 += delta * (x - a.mean)
	return nil
}

// Count returns the number of accepted samples (including any
// non-finite ones). Rejected NaN samples are not counted.
func (a *Accumulator) Count() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.count
}

// Skipped returns the number of rejected NaN samples.
func (a *Accumulator) Skipped() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.skipped
}

// Mean returns the running mean. It returns ErrStatsUnavailable if
// a non-finite sample has been seen, or ErrNoSamples if empty.
func (a *Accumulator) Mean() (float64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.poisoned {
		return 0, ErrStatsUnavailable
	}
	if a.count == 0 {
		return 0, ErrNoSamples
	}
	return a.mean, nil
}

// Variance returns the population variance M2/n. It returns
// ErrStatsUnavailable if poisoned, or ErrNoSamples if empty.
// The result is never negative.
func (a *Accumulator) Variance() (float64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.poisoned {
		return 0, ErrStatsUnavailable
	}
	if a.count == 0 {
		return 0, ErrNoSamples
	}
	return clampVariance(a.m2 / float64(a.count)), nil
}

// SampleVariance returns the unbiased sample variance M2/(n-1).
// It returns ErrStatsUnavailable if poisoned, ErrNoSamples if
// empty, and ErrInsufficientSamples if exactly one sample has been
// seen (zero degrees of freedom). The result is never negative.
func (a *Accumulator) SampleVariance() (float64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.poisoned {
		return 0, ErrStatsUnavailable
	}
	if a.count == 0 {
		return 0, ErrNoSamples
	}
	if a.count == 1 {
		return 0, ErrInsufficientSamples
	}
	return clampVariance(a.m2 / float64(a.count-1)), nil
}

// clampVariance defensively maps a (theoretically impossible) tiny
// negative float to exact zero so variance is never negative.
func clampVariance(v float64) float64 {
	if v < 0 {
		return 0
	}
	return v
}

// snapshot returns a consistent copy of the internal state.
func (a *Accumulator) snapshot() snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	return snapshot{
		count:    a.count,
		mean:     a.mean,
		m2:       a.m2,
		skipped:  a.skipped,
		poisoned: a.poisoned,
	}
}
