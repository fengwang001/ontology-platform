// Package api is the façade over sched/dep: atomic append, depths, rounds, parallelism, replay, self check.
package api

import (
	"errors"
	"sync"

	"ontology/dep"
	"ontology/sched"
)

var (
	ErrBadParams  = errors.New("api: p and maxTxns must be positive")
	ErrSeqGap     = errors.New("api: seq gap inside or before batch")
	ErrInvalidTxn = errors.New("api: empty write set or empty key in txn")
	ErrCapacity   = errors.New("api: accepted txn count would exceed maxTxns")
)

type Txn = dep.Txn

type Plan struct {
	RoundOf []int
	Rounds  [][]int64
	Total   int
}

type Engine struct {
	mu   sync.RWMutex
	g    *dep.Graph
	p    int
	max  int
	plan *sched.Schedule
}

func New(p, maxTxns int) (*Engine, error) {
	if p <= 0 || maxTxns <= 0 {
		return nil, ErrBadParams
	}
	g := dep.New()
	return &Engine{g: g, p: p, max: maxTxns, plan: sched.Build(g, p)}, nil
}

// Append validates the whole batch first; any rejection changes no state.
func (e *Engine) Append(txns []Txn) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.g.Len()+len(txns) > e.max {
		return ErrCapacity
	}
	for i, t := range txns {
		if err := e.g.Validate(t, int64(e.g.Len()+i+1)); err != nil {
			return mapErr(err)
		}
	}
	for _, t := range txns {
		if err := e.g.Add(t); err != nil {
			return mapErr(err)
		}
	}
	e.plan = sched.Build(e.g, e.p)
	return nil
}

func mapErr(err error) error {
	switch {
	case errors.Is(err, dep.ErrSeqGap):
		return ErrSeqGap
	case errors.Is(err, dep.ErrEmptyWrites), errors.Is(err, dep.ErrEmptyKey):
		return ErrInvalidTxn
	default:
		return err
	}
}

func (e *Engine) Depth(seq int64) (d int, ok bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	_, _, d, ok = e.g.Record(seq)
	return
}
func (e *Engine) Rounds() Plan {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return Plan{e.plan.RoundOf, e.plan.Rounds, e.plan.Total}
}
func (e *Engine) MaxParallel() int { e.mu.RLock(); defer e.mu.RUnlock(); return sched.MaxParallel(e.g) }
func (e *Engine) Replay() map[string]int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.plan.Replay(e.g)
}

// SelfCheck rebuilds a fresh engine on a built-in sequence and verifies the four invariants.
func (e *Engine) SelfCheck() error {
	t, _ := New(2, 64)
	if err := t.Append([]Txn{
		{Seq: 1, Writes: []string{"a"}}, {Seq: 2, Writes: []string{"b"}, Reads: []string{"a"}},
		{Seq: 3, Writes: []string{"a", "c"}}, {Seq: 4, Writes: []string{"d"}, Reads: []string{"c"}},
		{Seq: 5, Writes: []string{"b", "d"}}, {Seq: 6, Writes: []string{"c"}, Reads: []string{"b"}},
		{Seq: 7, Writes: []string{"e"}, Reads: []string{"a"}}, {Seq: 8, Writes: []string{"b", "e"}},
	}); err != nil {
		return err
	}
	wd, wr := []int{1, 1, 2, 1, 2, 3, 1, 3}, []int{1, 1, 2, 2, 3, 3, 4, 5}
	p := t.Rounds()
	for i := 1; i <= 8; i++ {
		d, _ := t.Depth(int64(i))
		if d != wd[i-1] || p.RoundOf[i-1] != wr[i-1] {
			return errors.New("api: self check depth/round mismatch")
		}
	}
	if p.Total != 5 || t.MaxParallel() != 4 {
		return errors.New("api: self check total/maxparallel mismatch")
	}
	kv := t.Replay()
	for k, v := range map[string]int64{"a": 3, "b": 8, "c": 6, "d": 5, "e": 8} {
		if kv[k] != v {
			return errors.New("api: self check replay mismatch")
		}
	}
	return rejectProbe()
}

func rejectProbe() error {
	e, _ := New(2, 4)
	e.Append([]Txn{{Seq: 1, Writes: []string{"a"}}, {Seq: 2, Writes: []string{"b"}}})
	cases := []struct {
		b   []Txn
		err error
	}{
		{[]Txn{{Seq: 3, Writes: []string{"a"}}, {Seq: 5, Writes: []string{"c"}}}, ErrSeqGap},
		{[]Txn{{Seq: 3, Writes: nil}}, ErrInvalidTxn},
		{[]Txn{{Seq: 3, Writes: []string{"x", ""}}}, ErrInvalidTxn},
		{[]Txn{{Seq: 3, Writes: []string{"a"}}, {Seq: 4, Writes: []string{"b"}}, {Seq: 5, Writes: []string{"c"}}}, ErrCapacity},
	}
	for _, c := range cases {
		if !errors.Is(e.Append(c.b), c.err) {
			return errors.New("api: self check rejection mismatch")
		}
	}
	if _, ok := e.Depth(3); ok {
		return errors.New("api: rejected batch left a trace")
	}
	e.Append([]Txn{{Seq: 3, Writes: []string{"c"}}})
	if d, ok := e.Depth(3); !ok || d != 1 {
		return errors.New("api: engine unusable after rejection")
	}
	return nil
}
