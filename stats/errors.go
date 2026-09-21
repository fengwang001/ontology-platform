// Package stats provides an online, numerically stable, mergeable
// accumulator for streaming float64 samples.
package stats

import "errors"

// Sentinel errors returned for undefined or unavailable statistics.
// Callers can use errors.Is to distinguish the empty case from the
// single-sample case and to detect statistics that have been polluted
// by non-finite values.
var (
	// ErrNoSamples is returned when a statistic requires at least one
	// sample but the accumulator is empty (count == 0).
	ErrNoSamples = errors.New("stats: no samples")

	// ErrSingleSample is returned for sample variance when exactly one
	// sample is present, because the unbiased estimator has zero degrees
	// of freedom (n-1 == 0).
	ErrSingleSample = errors.New("stats: sample variance undefined for a single sample")

	// ErrStatsUnavailable is returned when the statistics have become
	// unusable because infinite samples propagated into mean or variance.
	// The accumulator must not silently hand back NaN or Inf results.
	ErrStatsUnavailable = errors.New("stats: statistics unavailable: non-finite result")
)
