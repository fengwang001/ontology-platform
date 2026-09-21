// Package ontology implements an online statistics accumulator for
// streaming float64 samples.
//
// The accumulator tracks count, mean, population variance and sample
// variance using Welford's numerically stable recurrence, and supports
// merging two independent accumulators with Chan's parallel algorithm.
// Merging is order-independent and bitwise commutative.
package ontology

import "errors"

// ErrNoSamples is returned by Mean, PopulationVariance and
// SampleVariance when the accumulator holds no samples.
var ErrNoSamples = errors.New("ontology: no samples")

// ErrDegenerateFreedom is returned by SampleVariance when the
// accumulator holds exactly one sample: the sample variance has zero
// degrees of freedom and is undefined.
var ErrDegenerateFreedom = errors.New("ontology: sample variance undefined for a single sample")

// ErrStatsUnavailable is returned when an infinite sample has entered
// the accumulator, making the running statistics unusable (mean is
// infinite or NaN). The error is sticky: once set, all statistic
// getters report it.
var ErrStatsUnavailable = errors.New("ontology: statistics unavailable (infinite sample seen)")
