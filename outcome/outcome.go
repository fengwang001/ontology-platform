// Package outcome classifies the result of a single downstream call.
package outcome

// Outcome is the classification of one call attempt.
type Outcome int

const (
	// Success means the wrapped function ran and succeeded.
	Success Outcome = iota
	// Failure means the wrapped function ran and failed.
	Failure
	// Rejected means the breaker refused to run the function at all.
	Rejected
)

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
