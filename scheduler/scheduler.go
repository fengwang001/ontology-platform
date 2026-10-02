// Package scheduler implements a single-core discrete-step preemption
// threshold scheduling model with wait-time aging and preemption protection.
package scheduler

import (
	"container/heap"
	"errors"
	"sync"
)

var (
	ErrInvalidConfig = errors.New("scheduler: invalid config")
	ErrInvalidTask   = errors.New("scheduler: invalid task parameters")
	ErrTaskExists    = errors.New("scheduler: task already exists")
	ErrFull          = errors.New("scheduler: task table full")
)

const infinity = 1 << 30

type taskState int

const (
	stateReady taskState = iota
	stateRunning
)

type task struct {
	id    int
	p     int
	th    int
	work  int // original workload
	rem   int
	seq   int
	epoch int // value of now at last wt reset; wt = now - epoch
	bonus int // current capped aging bonus
	e     int // cached effective priority p + bonus
	pc    int
	state taskState
	heap  int // index in ready heap, -1 when not in heap
}

type bonusEvent struct {
	t     *task
	epoch int // epoch the event was scheduled under
}

// Scheduler is a single-core preemption threshold scheduler. All methods are
// safe for concurrent use; results equal some serial order.
type Scheduler struct {
	mu       sync.Mutex
	w        int
	bmax     int
	m        int
	tmax     int
	now      int
	seq      int
	tasks    map[int]*task
	ready    readyHeap
	events   map[int][]*bonusEvent
	running  *task
	examined int
	logf     func(format string, args ...any)
}

// NewScheduler validates the configuration and returns a ready scheduler.
// W in [1,1e6], Bmax in [0,1000], M in [1,1000], Tmax in [1,1e6].
func NewScheduler(w, bmax, m, tmax int) (*Scheduler, error) {
	if w < 1 || w > 1_000_000 || bmax < 0 || bmax > 1000 ||
		m < 1 || m > 1000 || tmax < 1 || tmax > 1_000_000 {
		return nil, ErrInvalidConfig
	}
	return &Scheduler{
		w:      w,
		bmax:   bmax,
		m:      m,
		tmax:   tmax,
		tasks:  make(map[int]*task),
		events: make(map[int][]*bonusEvent),
	}, nil
}

// SetLogger installs an optional per-tick decision logger.
func (s *Scheduler) SetLogger(logf func(format string, args ...any)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logf = logf
}

// Now returns the current tick.
func (s *Scheduler) Now() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now
}

// Count returns the number of registered tasks (ready + running).
func (s *Scheduler) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.tasks)
}

// Add registers a task arriving at the current tick. Errors are reported in
// the order: invalid params, duplicate id, table full. A rejected Add changes
// nothing, including the seq counter.
func (s *Scheduler) Add(id, p, th, w int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id < 1 || p < 0 || p > 255 || th < p || th > 255 || w < 1 || w > 1_000_000 {
		return ErrInvalidTask
	}
	if _, ok := s.tasks[id]; ok {
		return ErrTaskExists
	}
	if len(s.tasks) >= s.tmax {
		return ErrFull
	}
	s.seq++
	t := &task{
		id:    id,
		p:     p,
		th:    th,
		work:  w,
		rem:   w,
		seq:   s.seq,
		epoch: s.now,
		e:     p,
		heap:  -1,
	}
	s.tasks[id] = t
	s.pushReady(t)
	return nil
}

// Step advances one tick and returns the runner id (0 when idle) and the id
// of the task completing at now+1 (0 when none).
func (s *Scheduler) Step() (run, done int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Phase 1: decision, using wt values as of the tick start.
	if sigma := s.peekSigma(); sigma != nil {
		s.examined++
		sh := infinity
		if s.running != nil && s.running.pc < s.m {
			sh = s.running.th
		}
		switch {
		case s.running == nil:
			s.dispatch(s.popSigma())
			s.log("tick=%d now=%d cand=%d e=%d sh=- action=dispatch (no runner)",
				s.now+1, s.now, sigma.id, sigma.e)
		case sigma.e > sh:
			prev := s.running
			prev.pc++
			prev.epoch = s.now
			prev.bonus = 0
			prev.e = prev.p
			s.pushReady(prev)
			s.dispatch(s.popSigma())
			s.log("tick=%d now=%d cand=%d e=%d sh=%d action=preempt prev=%d pc=%d",
				s.now+1, s.now, sigma.id, sigma.e, sh, prev.id, prev.pc)
		default:
			s.log("tick=%d now=%d cand=%d e=%d sh=%d action=continue runner=%d",
				s.now+1, s.now, sigma.id, sigma.e, sh, s.running.id)
		}
	} else {
		s.log("tick=%d now=%d cand=- e=- sh=- action=idle", s.now+1, s.now)
	}

	// Phase 2: execution. The runner burns one unit; ready tasks age by one
	// (implicitly, via epoch bookkeeping: wt = now - epoch).
	if s.running != nil {
		run = s.running.id
		s.running.rem--
		if s.running.rem == 0 {
			done = s.running.id
			delete(s.tasks, s.running.id)
			s.running = nil
		}
	}

	// Phase 3: clock advance.
	s.now++
	// Aging bonuses that take effect at the new tick start. Done here so
	// that cached effective priorities are current between Steps; the next
	// decision observes exactly the wt values of its tick start.
	s.applyBonusEvents()
	return run, done
}

// dispatch moves a ready task to the running state and resets its wait count.
func (s *Scheduler) dispatch(t *task) {
	t.state = stateRunning
	t.epoch = s.now
	t.bonus = 0
	t.e = t.p
	s.running = t
}

// applyBonusEvents raises the aging bonus of every ready task whose wait
// count reaches a multiple of W at the current tick.
func (s *Scheduler) applyBonusEvents() {
	evs := s.events[s.now]
	if len(evs) == 0 {
		return
	}
	delete(s.events, s.now)
	for _, ev := range evs {
		t := ev.t
		if t.state != stateReady || t.epoch != ev.epoch {
			continue // stale: task left ready or its wt was reset
		}
		t.bonus++
		t.e = t.p + t.bonus
		heap.Fix(&s.ready, t.heap)
		if t.bonus < s.bmax {
			at := t.epoch + s.w*(t.bonus+1)
			s.events[at] = append(s.events[at], &bonusEvent{t: t, epoch: t.epoch})
		}
	}
}

func (s *Scheduler) log(format string, args ...any) {
	if s.logf != nil {
		s.logf(format, args...)
	}
}
