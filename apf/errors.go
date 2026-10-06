// Package apf implements a server-side priority and fairness admission
// controller: flow classification, share-based concurrency seat allocation,
// inter-flow round-robin dequeuing, head-of-line blocking for wide requests,
// and queue timeouts. Time is injected by the caller so behavior is
// deterministic and reproducible.
package apf

// Kind identifies the category of an Error. Kinds are declared in decreasing
// order of precedence: when several error conditions hold simultaneously,
// only the one with the highest precedence (declared first) is reported.
type Kind int

const (
	// KindInvalidArgument: malformed request, lease or configuration.
	KindInvalidArgument Kind = iota + 1
	// KindClockSkew: injected time is earlier than the last accepted operation.
	KindClockSkew
	// KindNoMatch: no classification rule matched the request.
	KindNoMatch
	// KindUnsatisfiable: the request can never fit in its level's nominal seats.
	KindUnsatisfiable
	// KindQueueFull: the level's queue length limit is reached.
	KindQueueFull
	// KindQueueTimeout: a queued request waited at least the queue timeout.
	KindQueueTimeout
)

func (k Kind) String() string {
	switch k {
	case KindInvalidArgument:
		return "invalid argument"
	case KindClockSkew:
		return "clock skew"
	case KindNoMatch:
		return "no matching rule"
	case KindUnsatisfiable:
		return "unsatisfiable seats"
	case KindQueueFull:
		return "queue full"
	case KindQueueTimeout:
		return "queue timeout"
	}
	return "unknown"
}

// Error is the single error type returned by this package. Use KindOf to
// inspect the category.
type Error struct {
	Kind    Kind
	Message string
}

func (e *Error) Error() string {
	if e.Message == "" {
		return e.Kind.String()
	}
	return e.Kind.String() + ": " + e.Message
}

// KindOf extracts the Kind of an error produced by this package.
func KindOf(err error) (Kind, bool) {
	if e, ok := err.(*Error); ok {
		return e.Kind, true
	}
	return 0, false
}

func errf(kind Kind, msg string) *Error { return &Error{Kind: kind, Message: msg} }
