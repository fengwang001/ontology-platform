package ontology

import "errors"

var (
	ErrObjectNotFound    = errors.New("ontology: object not found")
	ErrDeleteRestricted  = errors.New("ontology: delete rejected by restrict rule")
	ErrUndefinedLinkType = errors.New("ontology: undefined link type")
)
