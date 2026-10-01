package ontology

import (
	"errors"
	"strconv"
)

var (
	ErrInvalidTransaction = errors.New("invalid transaction")
	ErrInvalidMode        = errors.New("invalid lock mode")
	ErrInvalidNodeID      = errors.New("invalid node id")
	ErrNodeNotFound       = errors.New("node not registered")
	ErrNodeExists         = errors.New("node already registered")
	ErrParentNotFound     = errors.New("parent not registered")
	ErrMissingIntent      = errors.New("missing ancestor intent")
	ErrConflict           = errors.New("lock conflict")
	ErrLockNotHeld        = errors.New("lock not held")
	ErrDescendantLocked   = errors.New("descendant lock is held")
)

type MissingIntentError struct {
	Ancestor string
	Required Mode
}

func (err *MissingIntentError) Error() string {
	return ErrMissingIntent.Error() + ": ancestor " + err.Ancestor + " requires " + string(err.Required)
}

func (err *MissingIntentError) Is(target error) bool { return target == ErrMissingIntent }

type ConflictError struct {
	Transaction int
	Mode        Mode
}

func (err *ConflictError) Error() string {
	return ErrConflict.Error() + ": transaction " + strconv.Itoa(err.Transaction) + " holds " + string(err.Mode)
}

func (err *ConflictError) Is(target error) bool { return target == ErrConflict }
