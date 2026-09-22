// Package slot models a single in-flight request slot and its lifecycle.
package slot

// State is the lifecycle state of one slot.
type State uint8

const (
	// Pending waits for a matching response.
	Pending State = iota
	// Completed was finished exactly once by a matching response.
	Completed
	// TimedOut missed its deadline.
	TimedOut
	// Canceled was canceled by the caller.
	Canceled
)

// String returns a stable human-readable name for the state.
func (s State) String() string {
	switch s {
	case Pending:
		return "pending"
	case Completed:
		return "completed"
	case TimedOut:
		return "timed_out"
	case Canceled:
		return "canceled"
	default:
		return "unknown"
	}
}
