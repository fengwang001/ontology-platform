// Package retry implements a deterministic retry scheduler.
package retry

import "time"

// Policy configures the retry schedule.
type Policy struct {
	// MaxAttempts is the total attempt limit including the first try.
	// Values <= 0 are treated as 1.
	MaxAttempts int
	// Base is the first backoff interval.
	Base time.Duration
	// Factor multiplies the backoff each round. Values <= 1 mean no growth.
	Factor int
	// Cap bounds a single backoff interval. Values <= 0 mean unbounded.
	Cap time.Duration
	// JitterPct is the jitter percentage in [0, 100].
	JitterPct int
}

func (p Policy) normalized() Policy {
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = 1
	}
	if p.Factor <= 1 {
		p.Factor = 1
	}
	if p.JitterPct < 0 {
		p.JitterPct = 0
	}
	if p.JitterPct > 100 {
		p.JitterPct = 100
	}
	return p
}

// baseDelay returns the deterministic backoff for the k-th wait (k >= 1)
// before jitter is applied: Base * Factor^(k-1), bounded by Cap.
func (p Policy) baseDelay(k int) time.Duration {
	d := p.Base
	for i := 1; i < k; i++ {
		if p.Factor > 1 {
			next := d * time.Duration(p.Factor)
			if next < d { // overflow: saturate
				d = time.Duration(1<<63 - 1)
			} else {
				d = next
			}
		}
		if p.Cap > 0 && d >= p.Cap {
			return p.Cap
		}
	}
	if p.Cap > 0 && d > p.Cap {
		d = p.Cap
	}
	return d
}

// delay returns the k-th wait interval, applying bounded jitter on top of
// the deterministic base delay. rnd is consulted exactly once per call and
// only when jitter is enabled.
func (p Policy) delay(k int, rnd func() float64) time.Duration {
	d := p.baseDelay(k)
	if p.JitterPct == 0 {
		return d
	}
	j := float64(p.JitterPct) / 100
	factor := 1 + (2*rnd()-1)*j
	if factor < 0 {
		factor = 0
	}
	return time.Duration(float64(d) * factor)
}
