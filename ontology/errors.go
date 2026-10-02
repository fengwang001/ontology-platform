package ontology

import "errors"

var (
	// ErrEmptyKey reports an operation on the empty key.
	ErrEmptyKey = errors.New("ontology: empty key")
	// ErrRange reports an operand outside the allowed int64 range.
	ErrRange = errors.New("ontology: value out of range")
	// ErrNotHeld reports access to a sequence that is not currently held.
	ErrNotHeld = errors.New("ontology: snapshot not held")
)
