// Package age computes a stored response's current age and freshness
// lifetime for a shared cache. Time is supplied exclusively through an
// injected Clock; this package never reads the wall clock itself.
package age

import (
	"errors"

	"ontology/directive"
)

var (
	// ErrClockNotInjected reports that Decide was given a nil Clock.
	ErrClockNotInjected = errors.New("age: clock not injected")
	// ErrIncoherentTimes reports a response that arrived before it was sent.
	ErrIncoherentTimes = errors.New("age: response arrived before request (tResp < tReq)")
)

// Clock is the only source of the current time. All instants are seconds.
type Clock interface {
	Now() int64
}

// ClockFunc adapts a function into a Clock.
type ClockFunc func() int64

// Now implements Clock.
func (f ClockFunc) Now() int64 { return f() }

// Params carries the instants and header values that feed age calculation.
// HasAge indicates whether the Age header was present; AgeHdr is its seconds.
type Params struct {
	TReq   int64
	TResp  int64
	TDate  int64
	HasAge bool
	AgeHdr int64
}

func nonNeg(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}

// CurrentAge applies the age algorithm, clamping every subtraction at zero.
// The current instant is read solely from the injected clock.
func CurrentAge(clock Clock, p Params) (int64, error) {
	if clock == nil {
		return 0, ErrClockNotInjected
	}
	if p.TResp < p.TReq {
		return 0, ErrIncoherentTimes
	}
	apparent := nonNeg(p.TResp - p.TDate)
	corrected := apparent
	if p.HasAge && p.AgeHdr > corrected {
		corrected = p.AgeHdr
	}
	roundTrip := p.TResp - p.TReq
	resident := nonNeg(clock.Now() - p.TResp)
	return corrected + roundTrip + resident, nil
}

// Source identifies which header established the freshness lifetime.
type Source int

// Freshness sources in descending priority for a shared cache.
const (
	SourceNone Source = iota
	SourceSMaxAge
	SourceMaxAge
	SourceExpires
)

// Freshness selects the lifetime using the fixed chain
// s-maxage > max-age > Expires, defaulting to zero. Expires and HasExpires
// describe the Expires header as seconds; Expires-TDate is clamped to zero.
func Freshness(cc directive.Set, tDate int64, expires int64, hasExpires bool) (int64, Source) {
	if v, ok := cc.Delta("s-maxage"); ok {
		return v, SourceSMaxAge
	}
	if v, ok := cc.Delta("max-age"); ok {
		return v, SourceMaxAge
	}
	if hasExpires {
		return nonNeg(expires - tDate), SourceExpires
	}
	return 0, SourceNone
}
