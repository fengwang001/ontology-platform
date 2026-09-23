// Package outcome classifies a single downstream call.
// It depends on no other package in this module.
package outcome

// Outcome is the classification of one call attempt.
type Outcome int

const (
	// Success means the wrapped function returned without error.
	Success Outcome = iota
	// Failure means the wrapped function was invoked and returned an error
	// (or panicked).
	Failure
	// Rejected means the breaker refused to invoke the function at all.
	// Rejected calls never affect success/failure statistics.
	Rejected
)

// String returns the human-readable classification.
func (o Outcome) String() string {
	switch o {
	case Success:
		return "success"
	case Failure:
		return "failure"
	default:
		return "rejected"
	}
}
