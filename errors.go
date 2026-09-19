package ontology

import "errors"

// ErrInvalidCapacity is returned by New when k is not positive.
var ErrInvalidCapacity = errors.New("ontology: capacity K must be greater than zero")

// ErrInvalidDirection is returned by New when the direction is neither
// Desc nor Asc.
var ErrInvalidDirection = errors.New("ontology: direction must be Desc or Asc")

// IsInvalidCapacity reports whether err indicates a non-positive capacity.
func IsInvalidCapacity(err error) bool {
	return errors.Is(err, ErrInvalidCapacity)
}
