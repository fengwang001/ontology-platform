package ontology

import (
	"errors"
	"fmt"
)

type ErrorKind int

const (
	ErrUndefinedType ErrorKind = iota + 1
	ErrInvalidAlignment
	ErrDuplicateField
	ErrDirectCycle
	ErrSizeExceeded
	ErrDependentSizeExceeded
)

type TypeError struct {
	Kind ErrorKind
	Name string
	Err  error
}

func (e *TypeError) Error() string {
	if e == nil {
		return ""
	}
	if e.Name == "" {
		return e.Err.Error()
	}
	return fmt.Sprintf("%s: %s", e.Name, e.Err)
}
func (e *TypeError) Unwrap() error { return e.Err }

var (
	errUndefinedType    = errors.New("undefined type")
	errInvalidAlignment = errors.New("invalid alignment")
	errDuplicateField   = errors.New("duplicate field")
	errDirectCycle      = errors.New("direct embedding cycle")
	errSizeExceeded     = errors.New("size exceeded")
	errDependentSize    = errors.New("dependent size exceeded")
)

func typeError(kind ErrorKind, name string, err error) *TypeError {
	return &TypeError{Kind: kind, Name: name, Err: err}
}
