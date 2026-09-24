// Package verdict combines current age, freshness lifetime and the request /
// response cache directives into a final cache decision.
//
// Current time is supplied exclusively through an injected Clock; this package
// never calls time.Now.
package verdict

import (
	"errors"
	"time"

	"ontology/age"
	"ontology/directive"
)

// Clock supplies the current time.
type Clock interface {
	Now() time.Time
}

// ErrNoClock is returned when no clock was injected.
var ErrNoClock = errors.New("verdict: clock not injected")

// Outcome is the final decision for a stored response.
type Outcome string

const (
	Hit        Outcome = "直接命中"
	Revalidate Outcome = "必须再验证"
	Unusable   Outcome = "不可使用"
)

// Reason identifies which rule determined the outcome.
type Reason string

const (
	ReasonNoStore        Reason = "no-store"
	ReasonRespNoCache    Reason = "response no-cache"
	ReasonReqNoCache     Reason = "request no-cache"
	ReasonOnlyIfCached   Reason = "only-if-cached"
	ReasonFresh          Reason = "fresh"
	ReasonMaxStale       Reason = "max-stale"
	ReasonStale          Reason = "stale"
	ReasonReqMaxAge      Reason = "request max-age"
	ReasonMustRevalidate Reason = "must-revalidate"
)

// Input is everything needed to judge one stored response.
type Input struct {
	Times   age.Times
	Resp    directive.Set
	Req     directive.Set
	Expires time.Time // zero means no Expires header
	Clock   Clock
}

// Result is the decision with the deciding rule and the computed numbers.
type Result struct {
	Outcome   Outcome
	Reason    Reason
	Age       int64
	Freshness age.Freshness
}

// Decide evaluates a stored response.
func Decide(in Input) (Result, error) {
	if in.Clock == nil {
		return Result{}, ErrNoClock
	}
	now := in.Clock.Now()
	cur, err := age.CurrentAge(in.Times, now)
	if err != nil {
		return Result{}, err
	}
	fresh, err := age.FreshnessLifetime(in.Resp, in.Times, in.Expires)
	if err != nil {
		return Result{}, err
	}

	r := Result{Age: cur, Freshness: fresh}
	rev := false

	switch {
	case in.Resp.Has("no-store"):
		r.Outcome, r.Reason = Unusable, ReasonNoStore
		return r, nil
	case in.Resp.Bare("no-cache"):
		rev, r.Reason = true, ReasonRespNoCache
	case in.Req.Has("no-cache"):
		rev, r.Reason = true, ReasonReqNoCache
	default:
		r.Outcome, r.Reason, rev = freshnessRule(cur, fresh.Seconds, in.Req)
	}

	// must-revalidate overrides max-stale once stale.
	if !rev && in.Resp.Has("must-revalidate") && cur > fresh.Seconds {
		rev, r.Reason = true, ReasonMustRevalidate
	}
	// request max-age overrides an otherwise fresh hit.
	if !rev && in.Req.Has("max-age") {
		if n, _ := in.Req.Get("max-age"); cur > n {
			rev, r.Reason = true, ReasonReqMaxAge
		}
	}

	if rev {
		if in.Req.Has("only-if-cached") {
			r.Outcome, r.Reason = Unusable, ReasonOnlyIfCached
		} else {
			r.Outcome = Revalidate
		}
	} else if r.Outcome == "" {
		r.Outcome = Hit
	}
	return r, nil
}

func freshnessRule(cur, fresh int64, req directive.Set) (Outcome, Reason, bool) {
	minFresh := int64(0)
	if n, ok := req.Get("min-fresh"); ok {
		minFresh = n
	}
	if cur+minFresh < fresh {
		return Hit, ReasonFresh, false
	}
	if ms, ok := req.Get("max-stale"); ok {
		if ms == directive.MaxStaleAny || cur-fresh <= ms {
			return Hit, ReasonMaxStale, false
		}
	}
	return "", ReasonStale, true
}
