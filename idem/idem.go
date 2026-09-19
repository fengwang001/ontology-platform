// Package idem provides an idempotency-key executor: it guarantees that
// a function keyed by an idempotency key is executed at most once for a
// given request fingerprint, replays the cached outcome to subsequent
// callers, and single-flights concurrent callers.
package idem

// Result is the outcome of an executed function.
type Result struct {
	Code int
	Body string
}

// Outcome describes how a call to Do was served.
type Outcome int

const (
	// Executed means fn ran during this call.
	Executed Outcome = iota
	// Replayed means a cached completed record was returned.
	Replayed
	// Waited means this caller blocked on an in-flight call and
	// received its result.
	Waited
)

func (o Outcome) String() string {
	switch o {
	case Executed:
		return "Executed"
	case Replayed:
		return "Replayed"
	case Waited:
		return "Waited"
	default:
		return "Unknown"
	}
}
