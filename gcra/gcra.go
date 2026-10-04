// Package gcra implements the Generic Cell Rate Algorithm (virtual scheduling).
package gcra

// Headers are the rate-limit response headers produced for a request.
type Headers struct {
	Limit      int64
	Remaining  int64
	Reset      int64
	RetryAfter int64
}

// Decision is the outcome of a single GCRA evaluation.
type Decision struct {
	Allowed bool
	TAT     int64
	Headers Headers
}

// Judge evaluates one request against the bucket. tat<0 means no prior TAT.
// It assumes cost>=1, b>=1, t>=1, now>=0. Overflow is impossible for the
// documented ranges: new <= 1e15 + 1e12.
func Judge(t, b, cost, now, tat int64) Decision {
	a := tat
	if a < now {
		a = now
	}
	newTAT := a + cost*t
	burst := b * t

	h := Headers{Limit: b}
	if cost > b {
		// Impossible regardless of time or TAT; caller maps this to a
		// never-allowed error (and must not return these headers).
		return Decision{Allowed: false, TAT: tat, Headers: h}
	}
	if newTAT-now <= burst {
		elapsed := newTAT - now
		h.Remaining = (burst - elapsed) / t
		h.Reset = ceilDiv(elapsed, 1000)
		return Decision{Allowed: true, TAT: newTAT, Headers: h}
	}
	wait := a - now
	h.Remaining = (burst - wait) / t
	if h.Remaining < 0 {
		h.Remaining = 0
	}
	h.Reset = ceilDiv(wait, 1000)
	h.RetryAfter = ceilDiv(newTAT-now-burst, 1000)
	return Decision{Allowed: false, TAT: tat, Headers: h}
}

func ceilDiv(x, y int64) int64 {
	if x <= 0 {
		return 0
	}
	return (x + y - 1) / y
}
