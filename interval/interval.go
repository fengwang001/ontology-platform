// Package interval defines half-open [Start, End) intervals used on both
// the valid-time and transaction-time axes.
package interval

import "errors"

// ErrEmpty reports an interval whose start is not strictly before its end.
var ErrEmpty = errors.New("interval: empty interval (start >= end)")

// Infinity marks an open-ended interval ("valid until further notice").
// It is an ordinary large integer, so comparisons behave exactly like
// finite endpoints with no special cases.
const Infinity = int64(1<<63 - 1)

// Interval is a half-open interval [Start, End).
type Interval struct {
	Start int64
	End   int64
}

// New builds an interval and rejects empty or inverted ranges.
func New(start, end int64) (Interval, error) {
	if start >= end {
		return Interval{}, ErrEmpty
	}
	return Interval{Start: start, End: end}, nil
}

// Forever returns [start, Infinity).
func Forever(start int64) Interval {
	return Interval{Start: start, End: Infinity}
}

// Empty reports whether the interval contains no point.
func (i Interval) Empty() bool {
	return i.Start >= i.End
}

// Contains reports whether t lies in [Start, End): inclusive at the
// start, exclusive at the end.
func (i Interval) Contains(t int64) bool {
	return i.Start <= t && t < i.End
}

// Overlaps reports whether two intervals share at least one point.
func (i Interval) Overlaps(o Interval) bool {
	return i.Start < o.End && o.Start < i.End
}
