// Package scd maintains slowly changing dimension (SCD) history.
package scd

// OpenEnd is the sentinel end time of a left-closed, right-open interval
// whose right side is unbounded: [Start, OpenEnd).
const OpenEnd = int64(1<<63 - 1)

// Rejection reasons. Every reason is unique so callers can distinguish
// failure categories programmatically (errors.Is).
const (
	// ReasonNilEvent covers malformed arguments such as a nil event entry.
	ReasonNilEvent = "nil_event"
	// ReasonEmptyKey means an event carries an empty dimension key.
	ReasonEmptyKey = "empty_key"
	// ReasonTimeOutOfRange means EffectiveAt is outside [0, OpenEnd).
	ReasonTimeOutOfRange = "effective_at_out_of_range"
	// ReasonTooManyChangePoints means a key would exceed its change-point cap.
	ReasonTooManyChangePoints = "too_many_change_points"
)

// Event is a dimension change that may arrive out of order.
type Event struct {
	Key         string
	EffectiveAt int64
	Value       string
	// Delete marks a tombstone: the key has no value starting at EffectiveAt.
	Delete bool
}

// ChangePoint is a retained change point for one key.
type ChangePoint struct {
	At     int64
	Value  string
	Delete bool
}

// Interval is one left-closed right-open row [Start, End) of a key's history.
// End == OpenEnd means the interval extends to infinity.
type Interval struct {
	Key   string
	Start int64
	End   int64
	Value string
}

// BatchError describes why a whole batch was rejected. A rejected batch
// never mutates any stored change point or history interval.
type BatchError struct {
	Reason      string
	Index       int
	Key         string
	EffectiveAt int64
	Limit       int
	Detail      string
}

func (e *BatchError) Error() string { return "" }
