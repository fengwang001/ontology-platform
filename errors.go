package ontology

import "fmt"

// ConflictError reports a unique-constraint violation with full detail:
// which constraint, which existing record, the normalized key that
// collided, and both sides' raw (un-normalized) values.
type ConflictError struct {
	Constraint string           // name of the violated constraint
	Key        string           // normalized key that collided
	ExistingPK string           // primary key of the conflicting record
	Incoming   map[string]Value // caller's raw values (constraint columns)
	Existing   map[string]Value // stored raw values (constraint columns)
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("unique constraint %q violated: normalized key %q already held by record %q",
		e.Constraint, e.Key, e.ExistingPK)
}

// BatchError wraps a conflict detected while applying a batch, adding
// the positions of the operations involved.
type BatchError struct {
	OpIndex      int // index of the rejected op within the batch
	OtherOpIndex int // earlier batch op that inserted the key, -1 if pre-existing
	Conflict     *ConflictError
}

func (e *BatchError) Error() string {
	if e.OtherOpIndex >= 0 {
		return fmt.Sprintf("batch op %d conflicts with batch op %d: %s",
			e.OpIndex, e.OtherOpIndex, e.Conflict)
	}
	return fmt.Sprintf("batch op %d conflicts with stored record: %s",
		e.OpIndex, e.Conflict)
}

// Unwrap exposes the underlying ConflictError.
func (e *BatchError) Unwrap() error { return e.Conflict }
