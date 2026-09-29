package ontology

import (
	"errors"
	"fmt"
)

// Distinct, decidable error values. Callers can use errors.Is to classify.
var (
	// ErrEmptyBatch is returned when Apply receives no events.
	ErrEmptyBatch = errors.New("ontology: batch is empty")
	// ErrEmptyKey is returned when an event has an empty key.
	ErrEmptyKey = errors.New("ontology: event key is empty")
	// ErrInvalidOp is returned when an event carries an unknown operation.
	ErrInvalidOp = errors.New("ontology: event operation is invalid")
)

// BatchError describes a rejected batch. The batch is not applied.
type BatchError struct {
	// Index is the position of the first failing event within the batch.
	Index int
	// Key is the key targeted by the failing event.
	Key string
	// Op is the operation of the failing event.
	Op Op
	// Reason is the concrete cause: ErrEmptyKey, ErrInvalidOp, or
	// ErrPrecondition.
	Reason error
	// Expected is the event precondition: nil meant "must be absent".
	Expected *string
	// Actual is the value observed during rehearsal.
	Actual *string
}

func (e *BatchError) Error() string {
	return fmt.Sprintf("ontology: batch rejected at event %d (key=%q): %v", e.Index, e.Key, e.Reason)
}

func (e *BatchError) Unwrap() error {
	return e.Reason
}

// ErrPrecondition is returned when an event's expected-before condition
// does not hold during ordered rehearsal.
var ErrPrecondition = errors.New("ontology: precondition not satisfied")
