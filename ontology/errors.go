package ontology

import "fmt"

// ErrorKind classifies ontology errors for HTTP status mapping.
type ErrorKind string

const (
	ErrKindNotFound     ErrorKind = "NOT_FOUND"
	ErrKindConflict     ErrorKind = "CONFLICT"
	ErrKindInvalidInput ErrorKind = "INVALID_INPUT"
)

// Error is a typed ontology error.
type Error struct {
	Kind    ErrorKind
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Kind, e.Message) }

func notFound(format string, args ...any) *Error {
	return &Error{Kind: ErrKindNotFound, Message: fmt.Sprintf(format, args...)}
}

func conflict(format string, args ...any) *Error {
	return &Error{Kind: ErrKindConflict, Message: fmt.Sprintf(format, args...)}
}

func invalidInput(format string, args ...any) *Error {
	return &Error{Kind: ErrKindInvalidInput, Message: fmt.Sprintf(format, args...)}
}
