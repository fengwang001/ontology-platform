package ontology

import (
	"fmt"
	"strings"
)

// ConflictError reports a unique-constraint violation. It names
// the violated constraint, the primary key of the existing record,
// the normalized key they collided on, and both sides' original
// (un-normalized) values for display.
type ConflictError struct {
	// Constraint is the name of the violated constraint.
	Constraint string
	// ExistingID is the primary key of the conflicting record.
	ExistingID string
	// Key is the normalized key value that collided, formatted
	// as the normalized column values joined by " | ".
	Key string
	// Incoming holds the incoming record's original values for
	// the constraint columns, in column order.
	Incoming []Value
	// Existing holds the existing record's original values for
	// the constraint columns, in column order.
	Existing []Value
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("unique constraint %q violated: normalized key [%s] of new record conflicts with existing record %q (incoming %s vs existing %s)",
		e.Constraint, e.Key, e.ExistingID, formatValues(e.Incoming), formatValues(e.Existing))
}

func formatValues(vals []Value) string {
	parts := make([]string, len(vals))
	for i, v := range vals {
		if v.IsNull() {
			parts[i] = "NULL"
		} else {
			parts[i] = fmt.Sprintf("%q", v.String())
		}
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// BatchError reports that the op at Index failed, so the whole
// batch was rejected and nothing was applied. OtherIndex is the
// index of the earlier op in the same batch that owns the
// conflicting record, or -1 when that record pre-existed the
// batch.
type BatchError struct {
	Index      int
	OtherIndex int
	Err        *ConflictError
}

func (e *BatchError) Error() string {
	if e.OtherIndex >= 0 {
		return fmt.Sprintf("batch rejected: op %d conflicts with op %d: %v", e.Index, e.OtherIndex, e.Err)
	}
	return fmt.Sprintf("batch rejected: op %d: %v", e.Index, e.Err)
}

// Unwrap returns the underlying conflict error.
func (e *BatchError) Unwrap() error { return e.Err }
