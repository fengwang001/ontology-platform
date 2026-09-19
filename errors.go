package coercion

import (
	"errors"
	"fmt"
	"strings"
)

// ErrorCategory is a stable, mode-independent coercion failure class.
type ErrorCategory string

const (
	// Missing means the requested key was absent.
	Missing ErrorCategory = "missing"
	// Null means the value was explicitly nil.
	Null ErrorCategory = "null"
	// Invalid means the value cannot legally become the target type.
	Invalid ErrorCategory = "invalid"
	// Overflow means a value exceeded the target numeric range.
	Overflow ErrorCategory = "overflow"
	// PrecisionLoss means a mathematically defined conversion lost information.
	PrecisionLoss ErrorCategory = "precision_loss"
)

// Error is a categorized conversion failure.
type Error struct {
	// Category is the stable failure class.
	Category ErrorCategory
	// Target is the requested target type.
	Target Kind
	// Index is >= 0 when this error belongs to a slice element.
	Index int
	// hasIndex distinguishes element zero from a scalar error.
	hasIndex bool
	// Original is the unconverted input.
	Original any
	// Converted is the lenient-mode degraded result, when available.
	Converted any
	// Detail explains the degradation.
	Detail string
	// skipped marks an element with no target-typed fallback.
	skipped bool
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString(string(e.Category))
	if e.hasIndex {
		fmt.Fprintf(&b, " at index %d", e.Index)
	}
	fmt.Fprintf(&b, " converting %T to %s", e.Original, e.Target)
	if e.Detail != "" {
		b.WriteString(": ")
		b.WriteString(e.Detail)
	}
	return b.String()
}

// Errors preserves every element error produced by one conversion.
type Errors []*Error

func (es Errors) Error() string {
	parts := make([]string, len(es))
	for i, err := range es {
		parts[i] = err.Error()
	}
	return strings.Join(parts, "; ")
}

// Categories returns the ordered, duplicated category sequence.
func (es Errors) Categories() []ErrorCategory {
	out := make([]ErrorCategory, len(es))
	for i, err := range es {
		out[i] = err.Category
	}
	return out
}

// CategoryOf reports the category of the first coercion error in err.
func CategoryOf(err error) (ErrorCategory, bool) {
	var ce *Error
	if errors.As(err, &ce) {
		return ce.Category, true
	}
	var es Errors
	if errors.As(err, &es) && len(es) > 0 {
		return es[0].Category, true
	}
	return "", false
}
