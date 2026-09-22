package ontology

import "errors"

// Sentinel errors returned by Accumulator queries. Use errors.Is to
// distinguish them.
var (
	// ErrNoSamples is returned when a statistic is requested from an
	// accumulator that has not accepted any sample yet.
	ErrNoSamples = errors.New("ontology: no samples")

	// ErrInsufficientSamples is returned by SampleVariance when fewer
	// than two samples are available (zero degrees of freedom).
	ErrInsufficientSamples = errors.New("ontology: insufficient samples for sample variance")

	// ErrStatsUnavailable is returned when a non-finite sample
	// (+Inf/-Inf) has entered the accumulator and the statistics can no
	// longer be represented as finite floats.
	ErrStatsUnavailable = errors.New("ontology: statistics unavailable after non-finite sample")

	// ErrNaNSample is returned by Add when the sample is NaN. The sample
	// is rejected and counted in Skipped.
	ErrNaNSample = errors.New("ontology: NaN sample rejected")
)
