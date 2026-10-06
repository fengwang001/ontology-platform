package ontology

import "fmt"

type ErrorKind int

const (
	KindInvalidArgument ErrorKind = iota
	KindClockRollback
	KindUnknownHandle
	KindDuplicateCancel
)

var (
	ErrInvalidArgument = &LoopError{Kind: KindInvalidArgument, Message: "invalid argument"}
	ErrClockRollback   = &LoopError{Kind: KindClockRollback, Message: "clock cannot move backwards"}
	ErrUnknownHandle   = &LoopError{Kind: KindUnknownHandle, Message: "handle does not exist"}
	ErrDuplicateCancel = &LoopError{Kind: KindDuplicateCancel, Message: "handle was already canceled"}
)

type LoopError struct {
	Kind    ErrorKind
	Message string
}

func (err *LoopError) Error() string {
	return err.Message
}

func invalidArgument(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidArgument, message)
}
