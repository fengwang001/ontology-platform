// Package seqwin implements a sliding-window replay detector: the
// receiver judges each arriving packet, by sequence number, as Fresh,
// Duplicate, TooOld, or Invalid. It does nothing else: no transport,
// no retransmission, no acknowledgements, no cryptography.
package seqwin

// Verdict is the outcome of judging one sequence number.
type Verdict int

const (
	// Fresh means the sequence number is seen for the first time and
	// has been recorded in the window.
	Fresh Verdict = iota
	// Duplicate means the sequence number lies inside the window and
	// has already been recorded.
	Duplicate
	// TooOld means the sequence number lies left of the window.
	TooOld
	// Invalid means the sequence number itself is illegal (e.g. 0).
	Invalid
)

// String returns a human-readable name for the verdict.
func (v Verdict) String() string {
	switch v {
	case Fresh:
		return "Fresh"
	case Duplicate:
		return "Duplicate"
	case TooOld:
		return "TooOld"
	case Invalid:
		return "Invalid"
	default:
		return "Unknown"
	}
}
