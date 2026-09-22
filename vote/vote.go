// Package vote collects first-phase ballots of a two-phase commit
// transaction and rules on the outcome. It depends on nothing else.
package vote

// Ballot is a participant's first-phase vote.
type Ballot int

const (
	BallotAgree Ballot = iota
	BallotReject
)

func (b Ballot) String() string {
	switch b {
	case BallotAgree:
		return "agree"
	case BallotReject:
		return "reject"
	default:
		return "unknown"
	}
}

// Verdict is the final ruling of the first phase.
type Verdict int

const (
	// VerdictUndecided is the zero value: no ruling has been made yet.
	VerdictUndecided Verdict = iota
	VerdictCommit
	VerdictAbort
)

func (v Verdict) String() string {
	switch v {
	case VerdictCommit:
		return "commit"
	case VerdictAbort:
		return "abort"
	default:
		return "undecided"
	}
}

// Reason explains why a verdict was reached.
type Reason int

const (
	// ReasonNone is the zero value: no reason while undecided.
	ReasonNone Reason = iota
	// ReasonAllAgreed means every participant voted agree.
	ReasonAllAgreed
	// ReasonRejected means at least one participant voted reject.
	ReasonRejected
	// ReasonTimeout means at least one participant failed to answer
	// before the deadline.
	ReasonTimeout
)

func (r Reason) String() string {
	switch r {
	case ReasonAllAgreed:
		return "all-agreed"
	case ReasonRejected:
		return "rejected"
	case ReasonTimeout:
		return "timeout"
	default:
		return "none"
	}
}

// Decision is the immutable outcome of the first phase. The zero value
// means "not decided yet".
type Decision struct {
	Verdict Verdict
	Reason  Reason
	// Culprit names the participant that rejected or timed out.
	// Empty when the verdict is commit.
	Culprit string
}
