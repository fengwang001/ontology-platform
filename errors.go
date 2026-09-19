package ontology

import (
	"errors"
	"time"
)

var (
	// ErrZeroFrom is returned when an interval starts at the zero time;
	// infinity is only allowed on the To side.
	ErrZeroFrom = errors.New("ontology: interval From must not be the zero time")

	// ErrEmptyInterval is returned when From equals To. It is distinct
	// from ErrReversedInterval (From strictly after To).
	ErrEmptyInterval = errors.New("ontology: empty interval (From == To)")

	// ErrReversedInterval is returned when From is strictly after To.
	ErrReversedInterval = errors.New("ontology: reversed interval (From > To)")

	// ErrTxNotAdvancing is returned when a write for an entity carries a
	// transaction time not strictly greater than the entity's last write.
	ErrTxNotAdvancing = errors.New("ontology: transaction time must be strictly greater than the previous write")
)

// ValidateValidInterval checks a valid-time interval and distinguishes the
// zero From, the empty interval, and the reversed interval.
func ValidateValidInterval(iv Interval) error {
	if iv.From.IsZero() {
		return ErrZeroFrom
	}
	if iv.To.IsZero() {
		return nil
	}
	if iv.From.Equal(iv.To) {
		return ErrEmptyInterval
	}
	if iv.From.After(iv.To) {
		return ErrReversedInterval
	}
	return nil
}

func validateWrite(from, to, at time.Time) error {
	if err := ValidateValidInterval(Interval{From: from, To: to}); err != nil {
		return err
	}
	if at.IsZero() {
		return ErrZeroFrom
	}
	return nil
}
