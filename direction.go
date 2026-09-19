package topk

import "errors"

// Direction controls which Score dimension is kept.
// The tie-break on equal scores is independent of the direction:
// IDs are always ordered lexicographically ascending.
type Direction int

const (
	// Desc keeps the K elements with the largest scores.
	Desc Direction = iota
	// Asc keeps the K elements with the smallest scores.
	Asc
)

func (d Direction) valid() bool {
	return d == Desc || d == Asc
}

// ErrInvalidCapacity is returned when K is not positive.
var ErrInvalidCapacity = errors.New("topk:capacity K must be greater than zero")

// ErrInvalidDirection is returned when the direction is neither Desc nor Asc.
var ErrInvalidDirection = errors.New("topk:direction must be topk.Desc or topk.Asc")
