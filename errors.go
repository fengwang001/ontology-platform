package quantile

import "errors"

var (
	// ErrInvalidProbability is returned by quantile queries when p is not in
	// [0,1]. NaN probabilities are invalid as well.
	ErrInvalidProbability = errors.New("quantile: probability p must be in [0,1]")

	// ErrEmpty is returned when a quantile is requested from a sketch that
	// contains no samples. It is distinct from ErrInvalidProbability.
	ErrEmpty = errors.New("quantile: sketch contains no samples")

	// ErrInvalidWeight is returned from Add when weight is zero, negative or
	// not an integer.
	ErrInvalidWeight = errors.New("quantile: weight must be a positive integer")

	// ErrNaNValue is returned from Add when value is NaN. NaN samples are
	// rejected, counted via SkippedNaN and never take part in sorting or
	// quantile results.
	ErrNaNValue = errors.New("quantile: NaN samples are not allowed")
)
