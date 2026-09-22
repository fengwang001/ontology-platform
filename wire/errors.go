// Package wire implements the low-level encoding primitives of the
// ontology message format: varints, field headers and length checks.
// It depends on nothing outside the standard library.
package wire

import "strconv"

// Kind identifies a class of decoding error. Kinds are mutually
// distinguishable via errors.Is with the package-level sentinels.
type Kind int

const (
	// KindVarintOverflow: a varint did not terminate within 10 bytes.
	KindVarintOverflow Kind = iota + 1
	// KindUnknownWireType: a wire type byte other than 0, 1 or 2.
	KindUnknownWireType
	// KindLength: a length prefix claims more bytes than remain.
	KindLength
	// KindFieldNumberZero: a field header used field number 0.
	KindFieldNumberZero
	// KindNestedLengthMismatch: inside a nested message the fields do
	// not consume exactly the declared nested length.
	KindNestedLengthMismatch
	// KindTruncated: the buffer ends in the middle of an element.
	KindTruncated
)

// Sentinels for errors.Is. Each carries only its Kind; actual errors
// returned by this package are *Error values with a concrete Offset.
var (
	ErrVarintOverflow       = &Error{Kind: KindVarintOverflow}
	ErrUnknownWireType      = &Error{Kind: KindUnknownWireType}
	ErrLength               = &Error{Kind: KindLength}
	ErrFieldNumberZero      = &Error{Kind: KindFieldNumberZero}
	ErrNestedLengthMismatch = &Error{Kind: KindNestedLengthMismatch}
	ErrTruncated            = &Error{Kind: KindTruncated}
)

// Error is a decoding failure at a concrete byte offset.
type Error struct {
	Kind   Kind
	Offset int
}

func (e *Error) Error() string {
	return "wire: " + e.Kind.String() + " at byte offset " + strconv.Itoa(e.Offset)
}

// Is matches by Kind, so errors.Is(err, wire.ErrLength) works for any
// *Error whose Kind is KindLength regardless of offset.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Kind == e.Kind
}

func (k Kind) String() string {
	switch k {
	case KindVarintOverflow:
		return "varint exceeds 10 bytes"
	case KindUnknownWireType:
		return "unknown wire type"
	case KindLength:
		return "length prefix exceeds remaining bytes"
	case KindFieldNumberZero:
		return "field number 0 is invalid"
	case KindNestedLengthMismatch:
		return "nested length does not match consumed bytes"
	case KindTruncated:
		return "buffer truncated"
	default:
		return "unknown error kind"
	}
}
