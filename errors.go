package ontology

import (
	"errors"
	"fmt"
)

// ErrNegativeK is returned when the threshold k is negative.
var ErrNegativeK = errors.New("ontology: threshold k must be >= 0")

// Side identifies which input string an error refers to.
type Side int

const (
	// SideNone is used when the notion of a side does not apply,
	// e.g. for the standalone RuneLen helper.
	SideNone Side = iota
	// SideLeft is the first (a) argument of Distance.
	SideLeft
	// SideRight is the second (b) argument of Distance.
	SideRight
)

func (s Side) String() string {
	switch s {
	case SideLeft:
		return "left"
	case SideRight:
		return "right"
	default:
		return "input"
	}
}

// UTF8Error reports invalid UTF-8 in an input string. It names the side
// and the byte offset of the first offending byte.
type UTF8Error struct {
	Side       Side
	ByteOffset int
}

func (e *UTF8Error) Error() string {
	return fmt.Sprintf("ontology: invalid UTF-8 on %s side at byte offset %d", e.Side, e.ByteOffset)
}
