package ontology

import "sync"

// Accumulator maintains running statistics over a stream of float64
// samples. The zero value is ready to use.
//
// Statistics are updated with Welford's recurrence:
//
//	delta = x - mean
//	mean += delta / n
//	m2   += delta * (x - mean)   // m2 = sum of squared deviations
//
// Population variance is m2/n, sample variance is m2/(n-1). Merging
// uses Chan's parallel algorithm, which is associative and, with the
// canonical operand ordering applied here, bitwise commutative.
//
// All methods are safe for concurrent use.
type Accumulator struct {
	mu      sync.Mutex
	n       int64
	mean    float64
	m2      float64
	skipped int64
	bad     bool
}

// New returns an empty Accumulator.
func New() *Accumulator {
	return &Accumulator{}
}

// Add feeds one sample into the accumulator. NaN samples are rejected
// and counted in Skipped. Infinite samples are accepted but mark the
// statistics as unavailable (see ErrStatsUnavailable).
func (a *Accumulator) Add(x float64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.addLocked(x)
}

// Count returns the number of accepted samples.
func (a *Accumulator) Count() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.n
}

// Skipped returns the number of rejected NaN samples.
func (a *Accumulator) Skipped() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.skipped
}

// Mean returns the running mean.
func (a *Accumulator) Mean() (float64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.meanLocked()
}

// PopulationVariance returns the running population variance (m2/n).
func (a *Accumulator) PopulationVariance() (float64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.populationVarianceLocked()
}

// SampleVariance returns the running sample variance (m2/(n-1)).
func (a *Accumulator) SampleVariance() (float64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sampleVarianceLocked()
}

// Merge returns a new Accumulator combining a and b. Neither operand
// is modified. Merge(a, b) and Merge(b, a) are bitwise identical.
func Merge(a, b *Accumulator) *Accumulator {
	sa := a.snapshot()
	sb := b.snapshot()
	return mergeSnapshots(sa, sb)
}
