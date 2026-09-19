package ontology

import "time"

// Interval is a half-open interval [From, To) on either time axis.
// A zero To denotes positive infinity ("open end").
type Interval struct {
	From time.Time
	To   time.Time
}

// Contains reports whether t belongs to [From, To): From is inclusive,
// To is exclusive. The zero To means infinity.
func (i Interval) Contains(t time.Time) bool {
	if t.Before(i.From) {
		return false
	}
	return i.To.IsZero() || t.Before(i.To)
}

// Overlaps reports whether two half-open intervals share any point.
// Touching at a boundary (a.To == b.From) is not an overlap.
func (i Interval) Overlaps(other Interval) bool {
	if !other.To.IsZero() && !i.From.Before(other.To) {
		return false
	}
	if !i.To.IsZero() && !other.From.Before(i.To) {
		return false
	}
	return true
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if b.IsZero() {
		return a
	}
	if a.IsZero() || a.After(b) {
		return b
	}
	return a
}
