package ontology

import (
	"errors"
	"fmt"
)

// ErrNegativeK is returned by Compare when the threshold k is negative.
var ErrNegativeK = errors.New("ontology: threshold k must be non-negative")

// UTF8Error reports an invalid UTF-8 byte sequence in one of the two
// input strings. Side is 'a' or 'b' identifying which argument failed,
// and Offset is the byte offset of the first invalid byte.
type UTF8Error struct {
	Side   byte
	Offset int
}

func (e *UTF8Error) Error() string {
	return fmt.Sprintf("ontology: invalid UTF-8 in argument %c at byte offset %d", e.Side, e.Offset)
}
