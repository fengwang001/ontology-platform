package ontology

import "errors"

var (
	ErrInvalidArgument  = errors.New("ontology: invalid argument")
	ErrNameRegistered   = errors.New("ontology: function name already registered")
	ErrFunctionLimit    = errors.New("ontology: function limit exceeded")
	ErrUnknownCallee    = errors.New("ontology: unknown callee")
	ErrArgumentCount    = errors.New("ontology: callee argument count mismatch")
	ErrFunctionNotFound = errors.New("ontology: function not found")
)
