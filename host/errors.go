package host

import "fmt"

// ErrorKind classifies a host normalization failure.
type ErrorKind string

const (
	// KindBadPort is a non-numeric or out-of-range port.
	KindBadPort ErrorKind = "bad-port"
	// KindBadIPv6 is a malformed bracketed IPv6 literal.
	KindBadIPv6 ErrorKind = "bad-ipv6"
	// KindBadBracket is an unmatched "[" or a bare ":".
	KindBadBracket ErrorKind = "bad-bracket"
	// KindEmptyHost is an empty authority host.
	KindEmptyHost ErrorKind = "empty-host"
)

// Error locates a host normalization failure.
type Error struct {
	Kind   ErrorKind
	Offset int
}

func (e *Error) Error() string {
	return fmt.Sprintf("host: %s at byte %d", e.Kind, e.Offset)
}

func kindErr(kind ErrorKind, off int) error { return &Error{Kind: kind, Offset: off} }

// IsErrorKind reports whether err is a host error of the given kind.
func IsErrorKind(err error, kind ErrorKind) bool {
	e, ok := err.(*Error)
	return ok && e.Kind == kind
}
