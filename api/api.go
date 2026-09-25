// Package api is the public entry point of the delayed task queue. It wraps
// sched and exposes New, Schedule, Cancel, Tick, Peek and SelfCheck; it
// depends only on sched.
package api

import (
	"errors"
	"reflect"
	"sort"
	"sync"

	"ontology/sched"
)

// Queue is the concurrency-safe delayed task queue.
type Queue struct {
	mu sync.Mutex
	sm *sched.Scheduler
}

func New() *Queue { return &Queue{sm: sched.New()} }
func (q *Queue) Schedule(id string, f int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.sm.Schedule(id, f)
}
func (q *Queue) Cancel(id string) error { q.mu.Lock(); defer q.mu.Unlock(); return q.sm.Cancel(id) }
func (q *Queue) Tick(now int64) ([]string, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.sm.Tick(now)
}
func (q *Queue) Peek() (string, int64, bool) { q.mu.Lock(); defer q.mu.Unlock(); return q.sm.Peek() }

// ent/oracle is the naive full-recompute reference (I1): every generation is
// kept, due tasks are sorted by (fire, seq), dead generations are dropped.
type ent struct {
	id   string
	fire int64
	seq  uint64
	dead bool
}

type oracle struct {
	e    []ent
	live map[string]bool
	seq  uint64
}

func newOracle() *oracle { return &oracle{live: map[string]bool{}} }

func (o *oracle) add(id string, f int64) bool {
	if id == "" || o.live[id] {
		return false
	}
	o.seq++
	o.e = append(o.e, ent{id, f, o.seq, false})
	o.live[id] = true
	return true
}

func (o *oracle) cancel(id string) bool {
	if !o.live[id] {
		return false
	}
	for i := range o.e {
		if o.e[i].id == id {
			o.e[i].dead = true
		}
	}
	delete(o.live, id)
	return true
}

func (o *oracle) due(now int64) []string {
	var d []ent
	for _, x := range o.e {
		if !x.dead && x.fire <= now {
			d = append(d, x)
		}
	}
	sort.Slice(d, func(i, j int) bool {
		return d[i].fire < d[j].fire || d[i].fire == d[j].fire && d[i].seq < d[j].seq
	})
	out := make([]string, 0, len(d))
	for _, x := range d {
		out = append(out, x.id)
		o.cancel(x.id)
	}
	return out
}

// SelfCheck replays the section-3 eight steps through both the queue and the
// naive oracle, then checks monotonic time and the four rejected-op cases,
// verifying invariants I1-I4 on fresh local state.
func (q *Queue) SelfCheck() bool {
	type step struct {
		k  int    // 0 schedule, 1 cancel, 2 tick
		id string // tick: ""
		v  int64  // schedule: fireAt, tick: now
	}
	script := []step{
		{0, "a", 5}, {0, "b", 3}, {0, "c", 5}, {0, "d", 2},
		{1, "b", 0}, {0, "b", 4}, {2, "", 5}, {2, "", 5},
	}
	o, qs, ticks := newOracle(), New(), [][]string{}
	for _, x := range script {
		switch x.k {
		case 0:
			if (qs.Schedule(x.id, x.v) == nil) != o.add(x.id, x.v) {
				return false
			}
		case 1:
			if (qs.Cancel(x.id) == nil) != o.cancel(x.id) {
				return false
			}
		case 2:
			g, err := qs.Tick(x.v)
			if err != nil || !reflect.DeepEqual(g, o.due(x.v)) { // I1 + I2
				return false
			}
			ticks = append(ticks, g)
		}
	}
	if len(ticks) != 2 || !reflect.DeepEqual(ticks[0], []string{"d", "b", "a", "c"}) || len(ticks[1]) != 0 {
		return false // derived result; equal now does not re-fire (I3)
	}
	if _, e := qs.Tick(4); !errors.Is(e, sched.ErrClockRewind) {
		return false // I3: backwards time rejected
	}
	// I4: four distinct decidable errors; afterwards state is still intact.
	qe := New()
	errs := []error{qe.Schedule("", 1)}
	_ = qe.Schedule("x", 1)
	errs = append(errs, qe.Schedule("x", 2), qe.Cancel("ghost"))
	_, _ = qe.Tick(5)
	_, re := qe.Tick(4)
	errs = append(errs, re)
	want := []error{sched.ErrEmptyID, sched.ErrDuplicateID, sched.ErrCancelNotActive, sched.ErrClockRewind}
	for i := range errs {
		if !errors.Is(errs[i], want[i]) {
			return false
		}
	}
	g, _ := qe.Tick(5)
	return len(g) == 0 // x fired on the first tick(5); the rejected rewind changed nothing
}
