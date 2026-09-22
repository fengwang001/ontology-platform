package saga

import (
	"errors"

	"ontology/journal"
	"ontology/step"
)

// Decidable sentinel errors. All public failures are one of these or wrap one.
var (
	ErrNoSteps          = errors.New("saga: step list is empty")
	ErrDuplicateKey     = errors.New("saga: duplicate step idempotency key")
	ErrMaxSteps         = errors.New("saga: maximum step count exceeded")
	ErrMaxStepRetries   = errors.New("saga: step retry count exceeds limit")
	ErrInstanceNotFound = errors.New("saga: instance not found")
	ErrInstanceRunning  = errors.New("saga: instance is already running")
	ErrJournalFull      = journal.ErrJournalFull
	ErrNilCompensation  = step.ErrNilCompensation
)

// NilCompensationError carries the offending step location and implements
// Is matching step.ErrNilCompensation.
type NilCompensationError struct {
	Index   int
	IdemKey string
}

func (e *NilCompensationError) Error() string {
	return step.ErrNilCompensation.Error() + ": " + e.IdemKey
}

// Is makes errors.Is(err, step.ErrNilCompensation) / ErrNilCompensation true.
func (e *NilCompensationError) Is(target error) bool {
	return target == step.ErrNilCompensation
}
