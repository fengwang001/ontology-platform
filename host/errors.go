package host

import "fmt"

// Kind classifies a host-authority failure.
type Kind int

const (
	// KindBracket means an IPv6 literal has mismatched or stray brackets.
	KindBracket Kind = iota + 1
	// KindIPv6 means the content inside brackets is not a legal IPv6 address.
	KindIPv6
	// KindPort means the port is not a decimal number or exceeds 65535.
	KindPort
	// KindEmpty means the authority contains no host.
	KindEmpty
)

func (k Kind) String() string {
	switch k {
	case KindBracket:
		return "mismatched ipv6 brackets"
	case KindIPv6:
		return "bad ipv6 literal"
	case KindPort:
		return "bad port"
	case KindEmpty:
		return "empty host"
	default:
		return "unknown host error"
	}
}

// Error is a host parsing error; Kind lets callers distinguish cases.
type Error struct {
	Kind   Kind
	Offset int
	Detail string
}

func (e *Error) Error() string {
	return fmt.Sprintf("host: %s at byte offset %d: %s", e.Kind, e.Offset, e.Detail)
}

func asError(err error) (*Error, bool) {
	e, ok := err.(*Error)
	return e, ok
}
