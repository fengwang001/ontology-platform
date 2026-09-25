// Package api is the public facade over refc + trial (api -> trial -> refc).
package api

import (
	"errors"
	"fmt"

	"ontology/refc"
	"ontology/trial"
)

type Obj = refc.Obj

// The three rejection kinds are pairwise-distinct decidable sentinels.
var (
	ErrInvalidObject = trial.ErrInvalidObject // unknown or already released
	ErrNoRootToDrop  = trial.ErrNoRootToDrop  // Unroot drops a nonexistent root
	ErrObjectLimit   = errors.New("api: live object limit exceeded")
)

const DefaultLimit = 1 << 20

type Recycle struct {
	h     *refc.Heap
	c     *trial.Collector
	limit int
}

func New(limit ...int) *Recycle {
	n := DefaultLimit
	if len(limit) > 0 && limit[0] > 0 {
		n = limit[0]
	}
	h := refc.New()
	return &Recycle{h: h, c: trial.NewCollector(h), limit: n}
}

// Root creates rc=1, child=nil; it fails with ErrObjectLimit at the live cap.
func (r *Recycle) Root() (Obj, error) {
	r.h.Lock()
	defer r.h.Unlock()
	if r.h.Len() >= r.limit {
		return 0, ErrObjectLimit
	}
	return r.h.Root().ID, nil
}
func (r *Recycle) Unroot(o Obj) error   { r.h.Lock(); defer r.h.Unlock(); return r.c.Unroot(o) }
func (r *Recycle) Point(a, b Obj) error { r.h.Lock(); defer r.h.Unlock(); return r.c.Point(a, b) }
func (r *Recycle) Collect() int         { r.h.Lock(); defer r.h.Unlock(); return r.c.Collect() }
func (r *Recycle) RefCount(o Obj) int {
	r.h.Lock()
	defer r.h.Unlock()
	n, _ := r.h.Get(o)
	if n == nil {
		return 0
	}
	return n.RC
}

// probe snapshots heap health under one lock: invariant error, orphan count
// and fingerprint; unexported, only SelfCheck/tests use it.
func (r *Recycle) probe() (error, int, string) {
	r.h.Lock()
	defer r.h.Unlock()
	return r.h.Diagnose(), r.h.Orphans(), r.h.Fingerprint()
}

func mustObj(o Obj, e error) Obj {
	if e != nil {
		panic(e)
	}
	return o
}
func mustOK(e error) {
	if e != nil {
		panic(e)
	}
}

// SelfCheck replays the built-in sequences (eight-step cycle, rescue 乙,
// self-loop 丙, all rejections) and verifies the four invariants.
func (r *Recycle) SelfCheck() (err error) {
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("selfcheck: %v", e)
		}
	}()
	g := New()
	// sane: conservation + no-dangling (Diagnose) and live==reachable (Orphans).
	sane := func(m string) {
		if e, orph, _ := g.probe(); e != nil {
			panic(e)
		} else if orph != 0 {
			panic(m)
		}
	}
	cc := func(n int) {
		if g.Collect() != n {
			panic("wrong collect count")
		}
	}
	is := func(e, w error, m string) {
		if !errors.Is(e, w) {
			panic(m)
		}
	}
	// 1. Eight-step A<->B cycle loses both roots (rc stays 1); Collect frees 2.
	A, B := mustObj(g.Root()), mustObj(g.Root())
	mustOK(g.Point(A, B))
	mustOK(g.Point(B, A))
	mustOK(g.Unroot(A))
	mustOK(g.Unroot(B))
	cc(2)
	sane("orphans after cycle collect")
	X, Y := mustObj(g.Root()), mustObj(g.Root()) // 2. rescue (乙)
	mustOK(g.Point(X, Y))
	mustOK(g.Point(Y, X))
	mustOK(g.Unroot(Y))
	sane("reachable Y seen as orphan")
	cc(0)
	mustOK(g.Unroot(X))
	cc(2)
	S := mustObj(g.Root()) // 3. unrooted self-loop (丙)
	mustOK(g.Point(S, S))
	mustOK(g.Unroot(S))
	cc(1)
	sane("orphans after self-loop collect")
	Z, W := mustObj(g.Root()), mustObj(g.Root()) // 4. three distinct rejections
	mustOK(g.Point(W, Z))
	mustOK(g.Unroot(Z))
	_, _, before := g.probe()
	is(g.Unroot(Z), ErrNoRootToDrop, "want ErrNoRootToDrop")
	is(g.Point(Obj(1<<40), Z), ErrInvalidObject, "want ErrInvalidObject(from)")
	is(g.Point(Z, Obj(1<<40)), ErrInvalidObject, "want ErrInvalidObject(to)")
	if _, _, f := g.probe(); f != before {
		panic("rejected op left a trace")
	}
	mustOK(g.Unroot(W))
	sane("orphans after rejection sequence")
	l := New(1) // object-limit: third distinct, trace-free sentinel
	Q := mustObj(l.Root())
	_, _, before = l.probe()
	_, e := l.Root()
	is(e, ErrObjectLimit, "want ErrObjectLimit")
	if _, _, f := l.probe(); f != before {
		panic("rejected Root left a trace")
	}
	mustOK(l.Point(Q, 0))
	return l.Unroot(Q)
}
