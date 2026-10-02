package match

import "errors"

var (
	ErrInvalidArgument      = errors.New("invalid argument")
	ErrDuplicateName        = errors.New("duplicate type or constructor name")
	ErrSessionNotFound      = errors.New("session not found")
	ErrUnknownConstructor   = errors.New("unknown constructor")
	ErrWrongConstructorType = errors.New("constructor does not belong to expected type")
	ErrConstructorArity     = errors.New("constructor pattern arity mismatch")
	ErrExpansionLimit       = errors.New("pattern expansion limit exceeded")
)
