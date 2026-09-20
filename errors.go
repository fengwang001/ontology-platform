package ontology

import (
	"errors"
	"fmt"
)

// ErrSnapshotReleased is returned when reading from a snapshot that has
// already been released.
var ErrSnapshotReleased = errors.New("ontology: snapshot already released")

// ErrVersionReclaimed is returned when a snapshot points to a version that
// has been reclaimed by GC.
var ErrVersionReclaimed = errors.New("ontology: version has been reclaimed")

// ErrVersionNotFound is returned when referencing a version that does not
// exist (unknown or already reclaimed).
var ErrVersionNotFound = errors.New("ontology: version not found")

// ValidationKind classifies why a single field failed validation.
type ValidationKind int

const (
	// KindUnknownField means the field name was never declared.
	KindUnknownField ValidationKind = iota
	// KindTypeMismatch means the value type does not match the declaration.
	KindTypeMismatch
	// KindOutOfRange means an integer value is outside [Min, Max].
	KindOutOfRange
)

func (k ValidationKind) String() string {
	switch k {
	case KindUnknownField:
		return "unknown field"
	case KindTypeMismatch:
		return "type mismatch"
	case KindOutOfRange:
		return "out of range"
	default:
		return "invalid"
	}
}

// ValidationError describes one field that failed validation. The whole
// update is rejected when any field fails.
type ValidationError struct {
	Field string
	Kind  ValidationKind
	Value any
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("ontology: field %q: %s (value %v)", e.Field, e.Kind, e.Value)
}
