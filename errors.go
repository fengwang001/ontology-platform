package ontology

import "errors"

// Write-time validation errors. ErrEmptyInterval and ErrInvertedInterval are
// deliberately distinct categories so callers can tell them apart.
var (
	// ErrValidFromZero is returned when a fact's valid-from is the zero
	// time. Infinity is only allowed on the "to" side of an interval.
	ErrValidFromZero = errors.New("ontology: valid-from must not be the zero time")
	// ErrEmptyInterval is returned when valid-from equals valid-to.
	ErrEmptyInterval = errors.New("ontology: valid interval is empty (from == to)")
	// ErrInvertedInterval is returned when valid-from is after valid-to.
	ErrInvertedInterval = errors.New("ontology: valid interval is inverted (from > to)")
	// ErrTxZero is returned when the transaction time of a write is zero.
	ErrTxZero = errors.New("ontology: transaction time must not be the zero time")
	// ErrTxRegression is returned when a write's transaction time is not
	// strictly greater than the entity's last write time. No state is
	// changed when this error is returned.
	ErrTxRegression = errors.New("ontology: transaction time must be strictly greater than the entity's last write")
)

// AsOf lookup failures. The three "not found" situations are distinct
// sentinel errors so callers can classify them with errors.Is.
var (
	// ErrNoFacts means the entity/property pair never had any fact.
	ErrNoFacts = errors.New("ontology: no facts recorded for entity/property")
	// ErrValidOutOfRange means facts exist but validAt falls outside every
	// known valid interval.
	ErrValidOutOfRange = errors.New("ontology: validAt falls outside every known valid interval")
	// ErrNotYetKnown means validAt is covered by some fact, but none of the
	// covering facts was known to the system at txAt.
	ErrNotYetKnown = errors.New("ontology: matching facts exist but none was known at txAt")
)
