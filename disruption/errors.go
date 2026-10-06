package disruption

import "errors"

// Kind classifies every rejection so callers can branch on it programmatically.
// The constants are ordered from highest to lowest precedence; when a single
// operation satisfies several rejection conditions, the smallest Kind wins.
type Kind int

const (
	// KindInvalidArgument: malformed input (empty ids, bad budget/percent,
	// duplicate pods in a batch, confirm/cancel on a non-evicting pod).
	KindInvalidArgument Kind = iota
	// KindClockBacktrack: now is earlier than a previously accepted time.
	KindClockBacktrack
	// KindPodNotFound: the pod does not exist.
	KindPodNotFound
	// KindNotEvictablePhase: pod is Succeeded or Failed.
	KindNotEvictablePhase
	// KindAlreadyEvicting: pod has an in-flight eviction (admit, or any
	// readiness mutation on such a pod).
	KindAlreadyEvicting
	// KindConflict: pod is matched by more than one budget in its namespace.
	KindConflict
	// KindInsufficient: budget disruption allowance is exhausted / violated.
	KindInsufficient
)

func (k Kind) String() string {
	switch k {
	case KindInvalidArgument:
		return "InvalidArgument"
	case KindClockBacktrack:
		return "ClockBacktrack"
	case KindPodNotFound:
		return "PodNotFound"
	case KindNotEvictablePhase:
		return "NotEvictablePhase"
	case KindAlreadyEvicting:
		return "AlreadyEvicting"
	case KindConflict:
		return "Conflict"
	case KindInsufficient:
		return "Insufficient"
	default:
		return "Unknown"
	}
}

// AdjudicationError carries the Kind plus the offending pod (for batches and
// single-pod operations) and a human-readable reason.
type AdjudicationError struct {
	Kind   Kind
	Pod    PodID
	HasPod bool
	Reason string
}

func (e *AdjudicationError) Error() string {
	if e.HasPod {
		return e.Kind.String() + ": pod " + e.Pod.Namespace + "/" + e.Pod.Name + ": " + e.Reason
	}
	return e.Kind.String() + ": " + e.Reason
}

func errf(kind Kind, reason string) *AdjudicationError {
	return &AdjudicationError{Kind: kind, Reason: reason}
}

func errPod(kind Kind, id PodID, reason string) *AdjudicationError {
	return &AdjudicationError{Kind: kind, Pod: id, HasPod: true, Reason: reason}
}

// KindOf extracts the Kind of an adjudication error, returning ok=false for
// nil or unrelated errors.
func KindOf(err error) (Kind, bool) {
	var ae *AdjudicationError
	if errors.As(err, &ae) {
		return ae.Kind, true
	}
	return 0, false
}
