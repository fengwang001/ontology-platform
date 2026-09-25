// Package iv defines the half-open interval type [Start, End)
// and its basic validation. It depends on no other package.
package iv

import (
	"errors"
	"fmt"
)

var (
	// ErrEmptyInterval reports a degenerate interval with Start >= End.
	ErrEmptyInterval = errors.New("iv: empty interval (start >= end)")
	// ErrInvertedInterval reports Start > End; it wraps ErrEmptyInterval,
	// so errors.Is(err, ErrEmptyInterval) holds for every Start >= End.
	ErrInvertedInterval = fmt.Errorf("%w: start > end", ErrEmptyInterval)
)

// Interval is the half-open integer value domain [Start, End):
// it contains every x with Start <= x < End.
type Interval struct {
	Start, End int
}

// New returns the interval [start, end), or ErrEmptyInterval if start >= end.
func New(start, end int) (Interval, error) {
	v := Interval{Start: start, End: end}
	return v, v.Validate()
}

// Must is New for compile-time-known valid bounds; it panics on error.
func Must(start, end int) Interval {
	v, err := New(start, end)
	if err != nil {
		panic(err)
	}
	return v
}

// Validate returns ErrEmptyInterval (or ErrInvertedInterval wrapping it)
// when the interval is empty, and nil otherwise.
func (v Interval) Validate() error {
	switch {
	case v.Start > v.End:
		return fmt.Errorf("iv: [%d, %d): %w", v.Start, v.End, ErrInvertedInterval)
	case v.Start == v.End:
		return fmt.Errorf("iv: [%d, %d): %w", v.Start, v.End, ErrEmptyInterval)
	}
	return nil
}
