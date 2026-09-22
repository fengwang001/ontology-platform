package ontology

import "errors"

// Sentinel errors returned by the accumulator. Use errors.Is to test
// for them; each condition maps to exactly one sentinel so callers can
// distinguish a too-small sample set from poisoned statistics.
var (
	// ErrNoSamples is returned by Mean, Variance and SampleVariance
	// when the accumulator has not accepted any sample yet.
	ErrNoSamples = errors.New("ontology: no samples")

	// ErrTooFewSamples is returned by SampleVariance when exactly one
	// sample has been accepted: the sample variance needs at least
	// two degrees of freedom. It is distinct from ErrNoSamples.
	ErrTooFewSamples = errors.New("ontology: sample variance requires at least 2 samples")

	// ErrStatsUnavailable is returned by every statistic read after a
	// positive or negative infinity has been added: the running mean
	// and M2 are no longer finite, so no statistic is trustworthy.
	ErrStatsUnavailable = errors.New("ontology: statistics unavailable (non-finite sample observed)")

	// ErrNaN is returned by Add when the sample is NaN. The sample is
	// rejected, counted in Skipped, and never touches the statistics.
	ErrNaN = errors.New("ontology: NaN sample rejected")
)
