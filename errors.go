package ontology

import "errors"

var (
	ErrInvalidBucketCount = errors.New("ontology: bucket count must be positive")
	ErrInvalidRange       = errors.New("ontology: lower bound must be less than upper bound")
	ErrNaNBound           = errors.New("ontology: histogram bounds must not be NaN")
	ErrInfBound           = errors.New("ontology: histogram bounds must be finite")
	ErrNaNSample          = errors.New("ontology: NaN sample is not accepted")
	ErrHistogramMismatch  = errors.New("ontology: histogram parameters do not match")
)
