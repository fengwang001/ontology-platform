package ontology

import "errors"

var (
	ErrInvalidSubject    = errors.New("ontology: invalid subject identifier")
	ErrAmbiguousCoverage = errors.New("ontology: traversal coverage cannot be uniquely determined")
)
