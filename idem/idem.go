// Package idem provides an idempotency-key executor: it guarantees that a
// function associated with an idempotency key runs at most once for a given
// request fingerprint, replays the stored outcome for repeat calls, and
// coalesces concurrent callers onto a single execution.
package idem

// Result is the value produced (and replayed) for an idempotency key.
type Result struct {
	Code int
	Body string
}

// Outcome describes how a call to Executor.Do was served.
type Outcome int

const (
	// Executed means fn ran during this call.
	Executed Outcome = iota
	// Replayed means a previously completed result was returned.
	Replayed
	// Waited means another goroutine was executing fn for the same key and
	// this call blocked until that execution completed.
	Waited
)

// String returns a human-readable name for the outcome.
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
