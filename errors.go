package instance

import "errors"

// Reason identifies the category of a write failure.
type Reason int

const (
	// ReasonUnknown is never returned by the store.
	ReasonUnknown Reason = iota
	// ReasonNotFound means the primary key never existed.
	ReasonNotFound
	// ReasonDeleted means the instance exists but is currently logically deleted.
	ReasonDeleted
	// ReasonVersionConflict means the instance is alive but the expected
	// version does not match its current version.
	ReasonVersionConflict
	// ReasonAlreadyExists means Create targeted a live instance.
	ReasonAlreadyExists
	// ReasonDuplicateInBatch means the same primary key appears more than
	// once in a single BatchWrite request.
	ReasonDuplicateInBatch
	// ReasonConstraint means an attribute value violated the value rules.
	ReasonConstraint
)

func (r Reason) String() string {
	switch r {
	case ReasonNotFound:
		return "not_found"
	case ReasonDeleted:
		return "deleted"
	case ReasonVersionConflict:
		return "version_conflict"
	case ReasonAlreadyExists:
		return "already_exists"
	case ReasonDuplicateInBatch:
		return "duplicate_in_batch"
	case ReasonConstraint:
		return "constraint_violation"
	default:
		return "unknown"
	}
}

// WriteError is returned by the single-key write operations.
type WriteError struct {
	// Reason is the category of the failure.
	Reason Reason
	// ObjectType is the type name of the targeted instance.
	ObjectType string
	// Key is the primary key value of the targeted instance.
	Key string
	// Expected is the version the caller believed it was writing against.
	// It is zero for Create and for failures that do not carry an expected
	// version (NotFound, AlreadyExists, Constraint).
	Expected int64
	// Actual is the current version recorded by the store, including the
	// version a logically deleted instance had when it was deleted.
	Actual int64
	// Err is the underlying cause, if any.
	Err error
}

func (e *WriteError) Error() string {
	msg := "instance " + e.ObjectType + "/" + e.Key + ": " + e.Reason.String()
	if e.Reason == ReasonVersionConflict {
		msg += ": expected version " + itoa(e.Expected) + ", actual version " + itoa(e.Actual)
	} else if e.Actual > 0 {
		msg += ": current version " + itoa(e.Actual)
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *WriteError) Unwrap() error { return e.Err }

// AsWriteError extracts a *WriteError from err, if present.
func AsWriteError(err error) (*WriteError, bool) {
	var we *WriteError
	if errors.As(err, &we) {
		return we, true
	}
	return nil, false
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
