// Package cepmatch keeps per-key state for CEP `A -> B within T`.
package cepmatch

import (
	"errors"
	"slices"
	"sync"

	"ontology/cepwin"
)

type Mode int

const Strict, Relaxed Mode = 1, 2

var (
	ErrBadMode         = errors.New("cepmatch: mode must be Strict or Relaxed")
	ErrTimeRegression  = errors.New("cepmatch: per-key TS must be non-decreasing")
	ErrPendingOverflow = errors.New("cepmatch: pending A queue exceeds maxPending")
)

type qOp struct {
	ev      cepwin.Event
	dropped bool // true=head removal (rollback prepends ev), false=append (pop)
}
type batchLog struct {
	ops             map[string][]qOp
	last0           map[string]cepwin.Event // starting last per touched key; zero Event = none
	matchesN, inspN int
}

type Engine struct {
	mode       Mode
	T          int64
	maxPending int
	mu         sync.Mutex
	pending    map[string][]cepwin.Event
	last       map[string]cepwin.Event
	matches    []cepwin.Match
	inspected  int // unexported probe: queue heads inspected for the latest event
}

func NewEngine(mode Mode, T int64, maxPending int) (*Engine, error) {
	if mode != Strict && mode != Relaxed {
		return nil, ErrBadMode
	}
	if err := cepwin.CheckParams(T, maxPending); err != nil {
		return nil, err
	}
	return &Engine{mode: mode, T: T, maxPending: maxPending,
		pending: map[string][]cepwin.Event{}, last: map[string]cepwin.Event{}}, nil
}

func (e *Engine) feedOne(lg *batchLog, ev cepwin.Event) (out []cepwin.Match, n int, err error) {
	if err = cepwin.ValidateEvent(ev); err != nil {
		return
	}
	prev, hadPrev := e.last[ev.Key]
	if _, seen := lg.last0[ev.Key]; !seen {
		lg.last0[ev.Key] = prev
	}
	if hadPrev && ev.TS < prev.TS {
		return nil, 0, ErrTimeRegression
	}
	if e.mode == Relaxed {
		q := e.pending[ev.Key]
		for len(q) > 0 {
			n++ // one pending A inspected per iteration: always the queue head
			if !cepwin.Expired(q[0].TS, ev.TS, e.T) {
				break
			}
			lg.ops[ev.Key] = append(lg.ops[ev.Key], qOp{ev: q[0], dropped: true})
			q = q[1:]
		}
		switch {
		case ev.Type == "A":
			if len(q) >= e.maxPending {
				return nil, n, ErrPendingOverflow
			}
			lg.ops[ev.Key] = append(lg.ops[ev.Key], qOp{ev: ev})
			q = append(q, ev)
		case ev.Type == "B" && len(q) > 0:
			lg.ops[ev.Key] = append(lg.ops[ev.Key], qOp{ev: q[0], dropped: true})
			out, q = []cepwin.Match{{A: q[0], B: ev}}, q[1:]
		}
		e.pending[ev.Key] = q
	} else if ev.Type == "B" && hadPrev && prev.Type == "A" && cepwin.InWindow(prev.TS, ev.TS, e.T) {
		out = []cepwin.Match{{A: prev, B: ev}}
	}
	e.last[ev.Key] = ev
	e.matches = append(e.matches, out...)
	return
}

func (e *Engine) rollback(lg *batchLog) {
	for k, ops := range lg.ops {
		q := e.pending[k]
		for i := len(ops) - 1; i >= 0; i-- {
			if ops[i].dropped {
				q = append([]cepwin.Event{ops[i].ev}, q...)
			} else {
				q = q[:len(q)-1]
			}
		}
		e.pending[k] = q
	}
	for k, ev := range lg.last0 {
		if ev.Key == "" {
			delete(e.last, k)
		} else {
			e.last[k] = ev
		}
	}
	e.matches = e.matches[:lg.matchesN]
	e.inspected = lg.inspN
}

// Feed applies a batch atomically: any rejection leaves zero trace.
func (e *Engine) Feed(evs []cepwin.Event) ([]cepwin.Match, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	lg := &batchLog{map[string][]qOp{}, map[string]cepwin.Event{}, len(e.matches), e.inspected}
	out := make([]cepwin.Match, 0, len(evs))
	lastN := 0
	for _, ev := range evs {
		ms, n, err := e.feedOne(lg, ev)
		if err != nil {
			e.rollback(lg)
			return nil, err
		}
		out, lastN = append(out, ms...), n
	}
	e.inspected = lastN // empty batch leaves the probe at 0
	return out, nil
}

func (e *Engine) Matches() []cepwin.Match {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]cepwin.Match(nil), e.matches...)
}

// ProbeHeadOnly feeds m A's then a B and returns a bool (never the count) that the unexported probe stayed constant.
func ProbeHeadOnly(m int) bool {
	e, _ := NewEngine(Relaxed, 1<<60, m+10)
	e.Feed(slices.Repeat([]cepwin.Event{{Key: "k", Type: "A", TS: 1}}, m))
	e.inspected = 0
	e.Feed([]cepwin.Event{{Key: "k", Type: "B", TS: 1}})
	return e.inspected <= 2
}
