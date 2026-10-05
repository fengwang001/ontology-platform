// Package plan implements the backfill planning policy: settle timed-out
// in-flight requests, then emit new requests for the lowest requestable
// gaps, cut by Lm, limited by K and the per-call budget.
//
// Because the ingest package keeps the requestable set free of adjacent
// equal-c segments, the front of that set is always the next segment to
// cut, so Plan examines at most len(requests)+1 segments plus the segments
// settled during timeout processing.
package plan

import (
	"ontology/ingest"
)

// MaxBudget is the largest accepted per-call budget.
const MaxBudget = 1_000_000

// Request is one backfill request for the inclusive range [Lo, Hi].
type Request = ingest.Request

// Plan settles timed-out requests for dev at time now and emits at most K
// new requests covering at most budget sequence numbers. It returns the
// requests in ascending order (possibly empty). Rejections are reported in
// the order ErrInvalid > ErrClockBack > ErrNoDevice and change no state.
func Plan(e *ingest.Engine, dev string, now int64, budget int) ([]Request, error) {
	p := e.Params()
	if budget < 1 || budget > MaxBudget || now < 0 || now > ingest.MaxNow {
		return nil, ingest.ErrInvalid
	}
	e.Lock()
	defer e.Unlock()
	if err := e.CheckClock(now); err != nil {
		return nil, err
	}
	d, ok := e.Device(dev)
	if !ok {
		return nil, ingest.ErrNoDevice
	}
	timeouts := d.SettleTimeouts(now, p.Tq, p.R)
	var reqs []Request
	examined := 0
	remaining := int64(budget)
	for len(reqs) < p.K && remaining > 0 {
		g, ok := d.FirstRequestable()
		examined++
		if !ok {
			break
		}
		n := int64(p.Lm)
		if size := g.Hi - g.Lo + 1; size < n {
			n = size
		}
		if remaining < n {
			n = remaining
		}
		d.IssueRequest(g.Lo, n, g.C, now, e.NextGen())
		reqs = append(reqs, Request{Lo: g.Lo, Hi: g.Lo + n - 1})
		remaining -= n
	}
	d.NotePlanStats(examined, timeouts)
	e.AcceptNow(now)
	return reqs, nil
}
