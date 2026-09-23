// Package inherit computes transitive effective priorities over package mutex.
// Changes propagate waiter->holder and stop at the first unchanged node.
package inherit

import (
	"errors"

	"ontology/mutex"
)

var ErrNotOwner = errors.New("not the mutex owner")

// Handover reports a mutex handed to a waiter after its owner aborted.
type Handover struct{ Mu, From, To string }

// Meter counts the work of one mutating operation.
type Meter struct{ Visits, Cmps int }

type node struct {
	base, eff      int
	waitMu         string
	since, timeout int
	dead           bool
}
type tn struct {
	mu      string
	eff, pr int
	l, r    *tn
}
type bag struct {
	t *tn
	c *int
}

// bag is a treap multiset of per-mutex top-waiter contributions; only the max
// is read. treap priority derives deterministically from mu.
func rotR(t *tn) *tn { x := t.l; t.l = x.r; x.r = t; return x }
func rotL(t *tn) *tn { x := t.r; t.r = x.l; x.l = t; return x }

func prio(mu string) int {
	h := uint32(2166136261)
	for i := 0; i < len(mu); i++ {
		h ^= uint32(mu[i])
		h *= 16777619
	}
	return int(h)
}

func ins(t *tn, mu string, eff int) *tn {
	if t == nil {
		return &tn{mu, eff, prio(mu), nil, nil}
	}
	if mu == t.mu {
		t.eff = eff
		return t
	}
	left := eff > t.eff || eff == t.eff && mu < t.mu
	if left {
		t.l = ins(t.l, mu, eff)
		if t.l.pr > t.pr {
			t = rotR(t)
		}
	} else {
		t.r = ins(t.r, mu, eff)
		if t.r.pr > t.pr {
			t = rotL(t)
		}
	}
	return t
}

func del(t *tn, mu string) *tn {
	if t == nil {
		return nil
	}
	if mu < t.mu {
		t.l = del(t.l, mu)
	} else if mu > t.mu {
		t.r = del(t.r, mu)
	} else {
		switch {
		case t.l == nil:
			return t.r
		case t.r == nil:
			return t.l
		case t.l.pr > t.r.pr:
			t = rotR(t)
			t.r = del(t.r, mu)
		default:
			t = rotL(t)
			t.l = del(t.l, mu)
		}
	}
	return t
}

func maxt(t *tn) int {
	if t == nil {
		return -1
	}
	for t.l != nil {
		t = t.l
	}
	return t.eff
}

// Engine owns tasks, mutexes and effective priorities.
type Engine struct {
	nodes        map[string]*node
	mus          map[string]*mutex.Mutex
	owner        map[string]string
	held         map[string][]string
	bag          map[string]*bag
	visits, cmps int
}

// NewEngine returns an empty engine.
func NewEngine() *Engine {
	return &Engine{nodes: map[string]*node{}, mus: map[string]*mutex.Mutex{},
		owner: map[string]string{}, held: map[string][]string{}, bag: map[string]*bag{}}
}

// AddTask registers a task with a base priority.
func (e *Engine) AddTask(id string, base int) {
	e.nodes[id] = &node{base: base, eff: base}
	e.bag[id] = &bag{c: &e.cmps}
}

func (e *Engine) Eff(id string) int          { return e.nodes[id].eff }
func (e *Engine) Base(id string) int         { return e.nodes[id].base }
func (e *Engine) Dead(id string) bool        { return e.nodes[id].dead }
func (e *Engine) WaitingOn(id string) string { return e.nodes[id].waitMu }
func (e *Engine) Free(mu string) bool        { return e.mus[mu].Free() }
func (e *Engine) Owner(mu string) string     { return e.owner[mu] }
func (e *Engine) HeldBy(id string) []string  { return e.held[id] }

// WaiterLen returns the number of tasks blocked on a mutex.
func (e *Engine) WaiterLen(mu string) int { return e.mus[mu].Len() }

// Meter returns and resets per-operation counters.
func (e *Engine) Meter() Meter {
	m := Meter{e.visits, e.cmps}
	e.visits, e.cmps = 0, 0
	return m
}

// EnsureMu registers a mutex name.
func (e *Engine) EnsureMu(s string) {
	if _, ok := e.mus[s]; !ok {
		e.mus[s] = mutex.New(s)
	}
}

func (e *Engine) sync(mu string) {
	b := e.bag[e.owner[mu]]
	e.cmps++
	if w := e.mus[mu].Head(); w != nil {
		b.t = ins(b.t, mu, w.Eff)
	} else {
		b.t = del(b.t, mu)
	}
}

// cascade recomputes v from base + held-lock contributions; on change it
// follows v's wait edge upward, stopping at the first unchanged node.
func (e *Engine) cascade(v string) {
	for v != "" {
		n := e.nodes[v]
		e.visits++
		nv := n.base
		if t := maxt(e.bag[v].t); t > nv {
			nv = t
		}
		if nv == n.eff {
			return
		}
		n.eff = nv
		if n.waitMu == "" {
			return
		}
		e.mus[n.waitMu].Reorder(v, nv)
		e.sync(n.waitMu)
		v = e.owner[n.waitMu]
	}
}

// Acquire grants a free mutex directly.
func (e *Engine) Acquire(id, mu string) {
	e.owner[mu] = id
	e.held[id] = append(e.held[id], mu)
}

// BeginWait enqueues id on an owned mutex and starts inheritance.
func (e *Engine) BeginWait(id, mu string, step, timeout int) {
	n := e.nodes[id]
	n.waitMu, n.since, n.timeout = mu, step, timeout
	e.mus[mu].Enqueue(&mutex.Waiter{Task: id, Eff: n.eff, Since: step, Timeout: timeout})
	e.sync(mu)
	e.cascade(e.owner[mu])
}

// Unlock hands mu to its head waiter or frees it; returns the new owner.
func (e *Engine) Unlock(id, mu string) (string, error) {
	if e.owner[mu] != id {
		return "", ErrNotOwner
	}
	e.bag[id].t = del(e.bag[id].t, mu)
	e.drop(id, mu)
	w := e.mus[mu].Handoff()
	if w == nil {
		delete(e.owner, mu)
		e.cascade(id)
		return "", nil
	}
	wn := e.nodes[w.Task]
	wn.waitMu, wn.timeout = "", 0
	e.owner[mu] = w.Task
	e.held[w.Task] = append(e.held[w.Task], mu)
	e.sync(mu)
	e.cascade(w.Task)
	e.cascade(id)
	return w.Task, nil
}

func (e *Engine) leave(id string) {
	n, mu := e.nodes[id], e.nodes[id].waitMu
	if mu == "" {
		return
	}
	e.mus[mu].Remove(id)
	n.waitMu, n.timeout = "", 0
	e.sync(mu)
	e.cascade(e.owner[mu])
}

// Expire removes waiters whose k-step deadline is this step end. Handoff runs
// first in a step, so a granted head waiter is no longer present.
func (e *Engine) Expire(step int) []string {
	var out []string
	for id, n := range e.nodes {
		if n.waitMu != "" && n.timeout > 0 && step-n.since >= n.timeout {
			out = append(out, id)
		}
	}
	for _, id := range out {
		e.leave(id)
	}
	return out
}

// Abort ends a task, detaches its wait and force-releases all held mutexes to
// head waiters; recipients learn via returned Handovers.
func (e *Engine) Abort(id string) []Handover {
	n := e.nodes[id]
	if n.waitMu != "" {
		e.leave(id)
	}
	var hs []Handover
	var seeds []string
	for _, mu := range e.held[id] {
		e.bag[id].t = del(e.bag[id].t, mu)
		if w := e.mus[mu].Handoff(); w != nil {
			e.owner[mu] = w.Task
			e.held[w.Task] = append(e.held[w.Task], mu)
			e.nodes[w.Task].waitMu = ""
			e.sync(mu)
			seeds = append(seeds, w.Task)
			hs = append(hs, Handover{mu, id, w.Task})
		} else {
			delete(e.owner, mu)
		}
	}
	e.held[id] = nil
	for _, s := range seeds {
		e.cascade(s)
	}
	n.dead = true
	return hs
}

func (e *Engine) drop(id, mu string) {
	for i, m := range e.held[id] {
		if m == mu {
			e.held[id] = append(e.held[id][:i], e.held[id][i+1:]...)
			return
		}
	}
}
