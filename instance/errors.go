package instance

import (
	"errors"
	"fmt"
)

// ErrorKind classifies a store failure so callers can branch on it
// without parsing error strings.
type ErrorKind int

const (
	// KindConflict is a version mismatch on an instance that still exists.
	KindConflict ErrorKind = iota + 1
	// KindDeleted means the primary key was once created and later
	// logically deleted; it is distinct from KindNotFound.
	KindDeleted
	// KindNotFound means the primary key has never existed.
	KindNotFound
	// KindExists means Create targets a live primary key.
	KindExists
	// KindConstraint means the written properties violate the registered
	// property constraints.
	KindConstraint
	// KindDuplicateKey means the same primary key appears in more than
	// one operation of a single BatchWrite.
	KindDuplicateKey
)

func (k ErrorKind) String() string {
	switch k {
	case KindConflict:
		return "version conflict"
	case KindDeleted:
		return "instance deleted"
	case KindNotFound:
		return "not found"
	case KindExists:
		return "already exists"
	case KindConstraint:
		return "constraint violation"
	case KindDuplicateKey:
		return "duplicate key in batch"
	default:
		return "unknown"
	}
}

// Error is the structured error returned by store operations. For
// KindConflict, Expected holds the version the caller supplied and
// Actual holds the version currently stored.
type Error struct {
	Kind       ErrorKind
	ObjectType string
	Key        PrimaryKey
	Expected   int64
	Actual     int64
	Reason     string
}

func (e *Error) Error() string {
	switch e.Kind {
	case KindConflict:
		return fmt.Sprintf("%s: %s[%v] expected version %d, actual %d",
			e.Kind, e.ObjectType, e.Key, e.Expected, e.Actual)
	case KindConstraint:
		return fmt.Sprintf("%s: %s[%v]: %s",
			e.Kind, e.ObjectType, e.Key, e.Reason)
	default:
		return fmt.Sprintf("%s: %s[%v]", e.Kind, e.ObjectType, e.Key)
	}
}

func kindError(kind ErrorKind, ot string, key PrimaryKey) *Error {
	return &Error{Kind: kind, ObjectType: ot, Key: key}
}

// BatchError explains why a BatchWrite was rejected. Because the batch
// is atomic, no operation is applied when this is returned.
type BatchError struct {
	// Index is the position of the offending operation within the batch.
	Index int
	// Key is the primary key of that operation.
	Key PrimaryKey
	// Kind is the reason category. For duplicate keys it is
	// KindDuplicateKey; for per-operation failures it mirrors the
	// single-write error kind.
	Kind ErrorKind
	// Expected / Actual are populated for KindConflict.
	Expected int64
	Actual   int64
	Reason   string
}

func (e *BatchError) Error() string {
	switch e.Kind {
	case KindConflict:
		return fmt.Sprintf("batch rejected at op %d (key %v): %s: expected %d, actual %d",
			e.Index, e.Key, e.Kind, e.Expected, e.Actual)
	case KindConstraint:
		return fmt.Sprintf("batch rejected at op %d (key %v): %s: %s",
			e.Index, e.Key, e.Kind, e.Reason)
	default:
		return fmt.Sprintf("batch rejected at op %d (key %v): %s",
			e.Index, e.Key, e.Kind)
	}
}

// AsError lets errors.As recover the structured *Error describing a
// per-operation failure (not used for KindDuplicateKey).
func (e *BatchError) As(target any) bool {
	t, ok := target.(**Error)
	if !ok || e.Kind == KindDuplicateKey {
		return false
	}
	*t = &Error{Kind: e.Kind, Key: e.Key, Expected: e.Expected, Actual: e.Actual, Reason: e.Reason}
	return true
}

// Is supports sentinel-style checks via errors.Is, e.g.
// errors.Is(err, ErrVersionConflict).
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && e.Kind == t.Kind
}

// Sentinel errors for errors.Is. Inspect the concrete *Error for the
// expected/actual versions and key.
var (
	ErrVersionConflict = &Error{Kind: KindConflict}
	ErrDeleted         = &Error{Kind: KindDeleted}
	ErrNotFound        = &Error{Kind: KindNotFound}
	ErrAlreadyExists   = &Error{Kind: KindExists}
	ErrConstraint      = &Error{Kind: KindConstraint}
	ErrDuplicateKey    = &BatchError{Kind: KindDuplicateKey}
)

var _ = errors.Is
