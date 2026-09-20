// Package retry implements a deterministic retry scheduler.
package retry

import "time"

// Policy configures the retry schedule.
type Policy struct {
	MaxAttempts int           // total attempt limit (including the first); <=0 means 1
	Base        time.Duration // first backoff delay
	Factor      int           // multiplier per wait; <=1 means no growth
	Cap         time.Duration // per-wait ceiling; <=0 means no cap
	JitterPct   int           // jitter percentage, clamped to 0..100
}

func (p Policy) attempts() int {
	if p.MaxAttempts <= 0 {
		return 1
	}
	return p.MaxAttempts
}

func (p Policy) growth() int {
	if p.Factor <= 1 {
		return 1
	}
	return p.Factor
}

func (p Policy) jitter() int {
	if p.JitterPct < 0 {
		return 0
	}
	if p.JitterPct > 100 {
		return 100
	}
	return p.JitterPct
}

// baseDelay returns the pre-jitter delay for the k-th wait (1-based):
// Base*Factor^(k-1), capped at Cap when Cap > 0.
func (p Policy) baseDelay(k int) time.Duration {
	d := p.Base
	factor := int64(p.growth())
	for i := 1; i < k; i++ {
		if p.Cap > 0 && d >= p.Cap {
			return p.Cap
		}
		next := int64(d) * factor
		if next/factor != int64(d) { // overflow: saturate
			if p.Cap > 0 {
				return p.Cap
			}
			return time.Duration(1<<63 - 1)
		}
		d = time.Duration(next)
	}
	if p.Cap > 0 && d > p.Cap {
		return p.Cap
	}
	return d
}

// delay returns the actual delay for the k-th wait, applying jitter.
// rnd is consulted exactly once per wait, and only when jitter is on.
func (p Policy) delay(k int, rnd func() float64) time.Duration {
	d := p.baseDelay(k)
	j := p.jitter()
	if j == 0 || d <= 0 {
		return d
	}
	r := rnd()
	if r < 0 {
		r = 0
	}
	if r >= 1 {
		r = 1 - 1e-9
	}
	scale := 1 + (2*r-1)*float64(j)/100
	return time.Duration(float64(d) * scale)
}
