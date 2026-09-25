// Package api is the public front of the delayed task queue.
package api

import (
	"errors"
	"sort"
	"sync"

	"ontology/sched"
)

var (
	ErrEmptyID     = sched.ErrEmptyID
	ErrActiveID    = sched.ErrActiveID
	ErrUnknownID   = sched.ErrUnknownID
	ErrClockRewind = sched.ErrClockRewind
)

// Queue is safe for concurrent use by multiple goroutines.
type Queue struct {
	mu sync.Mutex
	s  *sched.S
}

func New() *Queue { return &Queue{s: sched.New()} }

func (q *Queue) Schedule(id string, fireAt int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.s.Schedule(id, fireAt)
}
func (q *Queue) Cancel(id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.s.Cancel(id)
}
func (q *Queue) Tick(now int64) ([]string, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.s.Tick(now)
}

func (q *Queue) Peek() (string, int64, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.s.Peek()
}

type mrec struct {
	id       string
	t        int64
	seq      int64
	canceled bool
}

type mop struct {
	k       byte // 's'/'c'/'t'
	id      string
	t       int64
	wantErr error
	peek    string // "?" skip, "-" none, else expected peek id
}

// SelfCheck replays a built-in sequence against a naive full-recomputation
// model, verifying invariants 1-4.
func (q *Queue) SelfCheck() bool {
	s := sched.New()
	var live []mrec
	var seq int64
	fireAt := map[string]int64{}
	ops := []mop{
		{'s', "a", 10, nil, "a"}, {'s', "b", 10, nil, "a"},
		{'s', "a", 11, ErrActiveID, "a"}, {'c', "z", 0, ErrUnknownID, "a"},
		{'s', "", 1, ErrEmptyID, "a"}, {'t', "", 5, nil, "a"},
		{'t', "", 3, ErrClockRewind, "a"}, {'t', "", 10, nil, "-"},
		{'c', "a", 0, ErrUnknownID, "-"}, {'s', "n", -5, nil, "n"},
		{'t', "", 10, nil, "-"}, {'t', "", 10, nil, "-"},
		{'s', "a", 7, nil, "a"}, {'c', "a", 0, nil, "-"},
		{'t', "", 20, nil, "-"},
	}
	for _, o := range ops {
		var got []string
		var err error
		switch o.k {
		case 's':
			err = s.Schedule(o.id, o.t)
		case 'c':
			err = s.Cancel(o.id)
		default:
			got, err = s.Tick(o.t)
		}
		if !errors.Is(err, o.wantErr) {
			return false
		}
		if err != nil {
			continue // rejected op: state untouched, model not fed either
		}
		switch o.k {
		case 's':
			seq++
			live = append(live, mrec{o.id, o.t, seq, false})
			fireAt[o.id] = o.t
		case 'c':
			for i := range live {
				if live[i].id == o.id {
					live[i].canceled = true
				}
			}
		default:
			for i := 1; i < len(got); i++ { // invariant 2
				if fireAt[got[i-1]] > fireAt[got[i]] {
					return false
				}
			}
			kept, due := live[:0], []mrec{}
			for _, r := range live { // naive full recomputation
				if r.canceled {
					continue
				}
				if r.t <= o.t {
					due = append(due, r)
				} else {
					kept = append(kept, r)
				}
			}
			sort.Slice(due, func(i, j int) bool {
				if due[i].t != due[j].t {
					return due[i].t < due[j].t
				}
				return due[i].seq < due[j].seq
			})
			if len(got) != len(due) {
				return false
			}
			for i, r := range due {
				if got[i] != r.id {
					return false
				}
			}
			live = kept
		}
		if o.peek != "?" {
			id, _, ok := s.Peek()
			if (o.peek == "-" && ok) || (o.peek != "-" && (!ok || id != o.peek)) {
				return false
			}
		}
	}
	return len(live) == 0
}
