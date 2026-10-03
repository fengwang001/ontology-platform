package cond

import "errors"

const MaxTime = int64(1_000_000_000_000)

// ErrInvalidArgument is returned for out-of-range condition parameters.
var ErrInvalidArgument = errors.New("invalid argument")

// Cond is a set of optional preconditions evaluated against the current version
// of a key. Every supplied subcondition must hold.
type Cond struct {
	IfMatch           *string
	IfUnmodifiedSince *int64
	IfNoneMatchStar   bool
}

// View is the current-version view of a key: the version with the largest
// version number.
type View struct {
	Exists bool
	Marker bool
	Etag   string
	Mtime  int64
}

// Failure names a failed subcondition.
type Failure string

const (
	FailIfMatch           Failure = "IfMatch"
	FailIfUnmodifiedSince Failure = "IfUnmodifiedSince"
	FailIfNoneMatch       Failure = "IfNoneMatch"
)

// FailedConditionError wraps a precondition failure and the failing subcondition.
type FailedConditionError struct {
	Name Failure
}

func (e *FailedConditionError) Error() string { return "precondition failed: " + string(e.Name) }

// Validate checks the parameter ranges embedded in the condition. Only
// IfUnmodifiedSince carries a range constraint: 0 <= s <= 10^12.
func (c Cond) Validate() error {
	if c.IfUnmodifiedSince != nil && (*c.IfUnmodifiedSince < 0 || *c.IfUnmodifiedSince > MaxTime) {
		return ErrInvalidArgument
	}
	return nil
}

// Evaluate checks every supplied subcondition in IfMatch, IfUnmodifiedSince,
// IfNoneMatch order and reports the first failure. An empty Failure means pass.
func (c Cond) Evaluate(v View) Failure {
	if c.IfMatch != nil {
		if !v.Exists || v.Marker || v.Etag != *c.IfMatch {
			return FailIfMatch
		}
	}
	if c.IfUnmodifiedSince != nil {
		if !v.Exists || v.Marker || v.Mtime > *c.IfUnmodifiedSince {
			return FailIfUnmodifiedSince
		}
	}
	if c.IfNoneMatchStar {
		if v.Exists && !v.Marker {
			return FailIfNoneMatch
		}
	}
	return ""
}
