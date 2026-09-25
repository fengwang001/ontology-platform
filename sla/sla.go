// Package sla decides the verdict of a single completed request:
// end-to-end latency is End.ts - Begin.ts, and latency <= threshold is OK,
// latency > threshold is a violation. It depends on no other package.
package sla

// Verdict is the SLA classification of one completed request.
type Verdict int

const (
	// OK means the request completed within the threshold (latency == T is OK).
	OK Verdict = iota
	// Violation means the request completed after the threshold (latency > T).
	Violation
)

func (v Verdict) String() string {
	if v == OK {
		return "OK"
	}
	return "violation"
}

// Latency returns the end-to-end latency End.ts - Begin.ts.
// A negative result means End happened before Begin; callers must reject it.
func Latency(begin, endTs int64) int64 {
	return endTs - begin
}

// Classify reports whether a completed request with the given latency is OK.
// The boundary is exact: latency == threshold is OK, only > threshold violates.
func Classify(latency, threshold int64) Verdict {
	if latency <= threshold {
		return OK
	}
	return Violation
}
