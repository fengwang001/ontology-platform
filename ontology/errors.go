package ontology

import "errors"

// Errors distinguish the reasons a mutating or querying call is rejected.
var (
	ErrInvalidRange      = errors.New("ontology: AddRange requires lo <= hi")
	ErrRangeOutOfBounds  = errors.New("ontology: AddRange hi must not exceed 2^32")
	ErrSelectOutOfBounds = errors.New("ontology: Select k must be less than cardinality")
)
