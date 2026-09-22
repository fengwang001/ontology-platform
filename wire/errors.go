package wire

import (
	"errors"
	"fmt"
)

// Kind classifies a wire-format decoding error. The kinds are mutually
// exclusive so callers can tell exactly which syntax rule was violated.
type Kind int

const (
	// KindVarintOverflow: a varint is not terminated within 10 bytes.
	KindVarintOverflow Kind = iota
	// KindUnknownWireType: the wire-type byte is not varint/bytes/message.
	KindUnknownWireType
	// KindLengthOverflow: a length prefix claims more bytes than remain.
	KindLengthOverflow
	// KindFieldNumberZero: field number 0 is not allowed.
	KindFieldNumberZero
	// KindNestedLengthMismatch: a field inside a nested message crosses the
	// nested message's declared length boundary.
	KindNestedLengthMismatch
	// KindTruncated: the buffer ends in the middle of a header or varint.
	KindTruncated
)

var (
	ErrVarintOverflow       = errors.New("wire: varint not terminated within 10 bytes")
	ErrUnknownWireType      = errors.New("wire: unknown wire type")
	ErrLengthOverflow       = errors.New("wire: length prefix exceeds remaining bytes")
	ErrFieldNumberZero      = errors.New("wire: field number 0 is not allowed")
	ErrNestedLengthMismatch = errors.New("wire: nested message length does not match its content")
	ErrTruncated            = errors.New("wire: unexpected end of input")
)

var sentinels = map[Kind]error{
	KindVarintOverflow:       ErrVarintOverflow,
	KindUnknownWireType:      ErrUnknownWireType,
	KindLengthOverflow:       ErrLengthOverflow,
	KindFieldNumberZero:      ErrFieldNumberZero,
	KindNestedLengthMismatch: ErrNestedLengthMismatch,
	KindTruncated:            ErrTruncated,
}

// Error describes a decode failure. Offset is the absolute byte offset in
// the top-level input where the offending byte sequence starts.
type Error struct {
	Kind   Kind
	Offset int
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s (offset %d)", sentinels[e.Kind], e.Offset)
}

// Is lets errors.Is match the sentinel for the error's kind.
func (e *Error) Is(target error) bool {
	return sentinels[e.Kind] == target
}
