package instance

import (
	"errors"
	"time"
)

// Instance is one stored object: a stable object type name plus primary key,
// a version that increases monotonically from 1, and the last write time.
type Instance struct {
	// ObjectType is the stable type name.
	ObjectType string
	// Key is the primary key value, unique within an ObjectType.
	Key string
	// Version starts at 1 and increases by one on every successful write,
	// including resurrection after a logical delete.
	Version int64
	// Attributes are the stored properties. The store never retains a caller's
	// map; values are deep-copied on write and on read.
	Attributes map[string]any
	// LastWriteTime is the time of the most recent successful write.
	LastWriteTime time.Time
	// Deleted is true when the instance is logically deleted.
	Deleted bool
}

// OpKind selects the operation carried by a batch entry.
type OpKind int

const (
	// OpCreate inserts a new instance, or resurrects a logically deleted one
	// while continuing its version history.
	OpCreate OpKind = iota + 1
	// OpUpdate overwrites attributes of a live instance.
	OpUpdate
	// OpDelete logically deletes a live instance.
	OpDelete
)

// Op is one entry in a BatchWrite request.
type Op struct {
	// Kind is the operation to perform.
	Kind OpKind
	// ObjectType is the stable type name.
	ObjectType string
	// Key is the primary key value.
	Key string
	// ExpectedVersion must equal the current version for Update and Delete.
	// It is ignored for Create.
	ExpectedVersion int64
	// Attributes is the full attribute set for Create and Update.
	Attributes map[string]any
}

// BatchError reports why a BatchWrite was rejected. A BatchWrite never has
// partial effects, so a non-nil BatchError means nothing was written.
type BatchError struct {
	// Index is the position of the offending entry in the request slice.
	Index int
	// ObjectType is the type name reported by the offending entry.
	ObjectType string
	// Key is the primary key reported by the offending entry.
	Key string
	// Reason is the category of the failure.
	Reason Reason
	// Expected and Actual carry the version mismatch details.
	Expected int64
	Actual   int64
	// Err is the underlying cause, if any.
	Err error
}

func (e *BatchError) Error() string {
	msg := "batch write rejected at index " + itoa(int64(e.Index)) +
		": " + e.ObjectType + "/" + e.Key + ": " + e.Reason.String()
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

func (e *BatchError) Unwrap() error { return e.Err }

// AsBatchError extracts a *BatchError from err, if present.
func AsBatchError(err error) (*BatchError, bool) {
	var be *BatchError
	if errors.As(err, &be) {
		return be, true
	}
	return nil, false
}
