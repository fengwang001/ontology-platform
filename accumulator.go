package ontology

import (
	"math"
	"sync"
)

// Accumulator is an online statistics accumulator for float64
// samples. The zero value is ready to use. All methods are safe for
// concurrent use; readers never observe a half-updated state because
// every mutation and every read happens under the same lock.
type Accumulator struct {
	mu       sync.RWMutex
	n        uint64
	mean     float64
	m2       float64
	skipped  uint64
	poisoned bool
}

// New returns an empty accumulator.
func New() *Accumulator {
	return &Accumulator{}
}

// Add feeds one sample into the accumulator.
//
// A NaN sample is rejected: it bumps the skipped counter and returns
// ErrNaN without touching any statistic. A positive or negative
// infinity is accepted and counted, but poisons the accumulator so
// that subsequent statistic reads return ErrStatsUnavailable. +0 and
// -0 are both legal samples and are treated as the number zero.
func (a *Accumulator) Add(x float64) error {
	if math.IsNaN(x) {
		a.mu.Lock()
		a.skipped++
		a.mu.Unlock()
		return ErrNaN
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.n++
	if math.IsInf(x, 0) {
		a.poisoned = true
		return nil
	}
	// Welford's online recurrence; see package documentation.
	delta := x - a.mean
	a.mean += delta / float64(a.n)
	a.m2 += delta * (x - a.mean)
	return nil
}

// Count returns the number of accepted (non-NaN) samples.
func (a *Accumulator) Count() uint64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.n
}

// Skipped returns the number of rejected NaN samples.
func (a *Accumulator) Skipped() uint64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.skipped
}

// Mean returns the running mean. It fails with ErrStatsUnavailable if
// an infinity has been observed, or with ErrNoSamples if empty.
func (a *Accumulator) Mean() (float64, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if err := a.usableLocked(); err != nil {
		return 0, err
	}
	return a.mean, nil
}

// Variance returns the population variance M2/n. It is never
// negative; identical samples yield exactly zero.
func (a *Accumulator) Variance() (float64, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if err := a.usableLocked(); err != nil {
		return 0, err
	}
	return clampVariance(a.m2 / float64(a.n)), nil
}

// SampleVariance returns the unbiased sample variance M2/(n-1). It
// fails with ErrTooFewSamples when fewer than two samples are present.
func (a *Accumulator) SampleVariance() (float64, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if err := a.usableLocked(); err != nil {
		return 0, err
	}
	if a.n < 2 {
		return 0, ErrTooFewSamples
	}
	return clampVariance(a.m2 / float64(a.n-1)), nil
}

// usableLocked reports whether any statistic can be served. The
// poisoned state dominates the empty state: a caller who fed an
// infinity must see ErrStatsUnavailable, not ErrNoSamples.
func (a *Accumulator) usableLocked() error {
	if a.poisoned {
		return ErrStatsUnavailable
	}
	if a.n == 0 {
		return ErrNoSamples
	}
	return nil
}

// clampVariance pins a tiny negative roundoff artifact to zero so a
// variance is never negative.
func clampVariance(v float64) float64 {
	if v < 0 {
		return 0
	}
	return v
}
