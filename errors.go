package cascade

type ErrorKind int

const (
	// InvalidArgument means an identifier, reference set, finalizer set, or policy is invalid.
	InvalidArgument ErrorKind = iota + 1
	// ObjectNotFound means the target object does not exist.
	ObjectNotFound
	// Conflict means an operation is forbidden while the object is deleting.
	Conflict
	// CycleDetected means owner references would form a cycle or self-reference.
	CycleDetected
	// OwnerMissing means a referenced owner is absent or deleting.
	OwnerMissing
)

// ControllerError is the typed error returned by controller operations.
type ControllerError struct {
	Kind    ErrorKind
	Message string
}

func (e ControllerError) Error() string { return e.Message }

func newError(kind ErrorKind, message string) ControllerError {
	return ControllerError{Kind: kind, Message: message}
}
