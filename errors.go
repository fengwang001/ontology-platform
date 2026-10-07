package ontology

import "errors"

var (
	ErrInvalidCaller   = errors.New("ontology: invalid caller identifier")
	ErrNotFound        = errors.New("ontology: object or type not found")
	ErrDuplicate       = errors.New("ontology: duplicate id")
	ErrInvalidArgument = errors.New("ontology: invalid argument")
)
