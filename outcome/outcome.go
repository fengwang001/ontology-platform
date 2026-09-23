// Package outcome classifies the result of a single wrapped call.
package outcome

// Kind is the classification of one call's result.
type Kind int

const (
	// Success means the wrapped function ran and returned nil.
	Success Kind = iota
	// Failure means the wrapped function ran and returned an error or panicked.
	Failure
	// Rejected means the call was refused and the function never ran.
	Rejected
)

func (k Kind) String() string {
	switch k {
	case Success:
		return "success"
	case Failure:
		return "failure"
	case Rejected:
		return "rejected"
	}
	return "unknown"
}
