// Package age computes a stored response's current age and freshness
// lifetime. All time is supplied by the caller; this package never reads the
// system clock.
package age

import (
	"errors"
	"time"

	"ontology/directive"
)

var (
	// ErrIncoherentTimestamps is returned when the response arrived before the
	// request was sent (tResp < tReq).
	ErrIncoherentTimestamps = errors.New("age: response time precedes request time")
	// ErrEmptyAndNoDate is returned when the response directives are empty and
	// the Date header time is absent.
	ErrEmptyAndNoDate = errors.New("age: empty cache control and no Date header")
)

// Times holds the timestamps (and the Age header) describing a response.
type Times struct {
	TReq   time.Time // request was sent
	TResp  time.Time // response arrived
	TDate  time.Time // Date header; zero means absent
	AgeHdr int64     // Age header seconds; < 0 treated as 0
}

// Source identifies which response directive supplied the freshness lifetime.
type Source string

const (
	SourceSMaxAge Source = "s-maxage"
	SourceMaxAge  Source = "max-age"
	SourceExpires Source = "expires"
	SourceNone    Source = "none"
)

// Freshness is the freshness lifetime and the directive it came from.
type Freshness struct {
	Seconds int64
	Source  Source
}

func clampZero(d time.Duration) int64 {
	if d < 0 {
		return 0
	}
	return int64(d / time.Second)
}

// CurrentAge computes the age in seconds at now. Every intermediate result is
// clamped at zero.
func CurrentAge(t Times, now time.Time) (int64, error) {
	if t.TResp.Before(t.TReq) {
		return 0, ErrIncoherentTimestamps
	}
	apparent := int64(0)
	if !t.TDate.IsZero() {
		apparent = clampZero(t.TResp.Sub(t.TDate))
	}
	header := t.AgeHdr
	if header < 0 {
		header = 0
	}
	corrected := apparent
	if header > corrected {
		corrected = header
	}
	resp := corrected + int64(t.TResp.Sub(t.TReq)/time.Second)
	resident := clampZero(now.Sub(t.TResp))
	return resp + resident, nil
}

// FreshnessLifetime applies the shared-cache precedence
// s-maxage > max-age > Expires > 0.
func FreshnessLifetime(resp directive.Set, t Times, expires time.Time) (Freshness, error) {
	if n, ok := resp.Get("s-maxage"); ok {
		return Freshness{Seconds: n, Source: SourceSMaxAge}, nil
	}
	if n, ok := resp.Get("max-age"); ok {
		return Freshness{Seconds: n, Source: SourceMaxAge}, nil
	}
	if !expires.IsZero() {
		if t.TDate.IsZero() {
			return Freshness{Seconds: 0, Source: SourceExpires}, nil
		}
		return Freshness{Seconds: clampZero(expires.Sub(t.TDate)), Source: SourceExpires}, nil
	}
	// No freshness directive at all.
	if len(resp.First()) == 0 && t.TDate.IsZero() {
		return Freshness{}, ErrEmptyAndNoDate
	}
	return Freshness{Seconds: 0, Source: SourceNone}, nil
}
