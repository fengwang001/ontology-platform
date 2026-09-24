package ontology

import "errors"

var (
	ErrEmptyRing     = errors.New("ontology: locate on empty ring")
	ErrInvalidVnodes = errors.New("ontology: vnodes must be a positive integer")
	ErrNodeExists    = errors.New("ontology: node already exists")
	ErrNodeNotFound  = errors.New("ontology: node not found")
)
