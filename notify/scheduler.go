// Package notify implements a deterministic notification retry scheduler
// with a channel fallback chain, Retry-After handling and per-channel
// circuit breaking.
package notify

import (
	"errors"
	"sync"
)

// Class is the failure class reported to Fail.
type Class string

const (
	ClassTransient Class = "transient"
	ClassThrottled Class = "throttled"
	ClassPermanent Class = "permanent"
)

// Reason is the dead-letter reason of a finished task.
type Reason string

const (
	ReasonNone      Reason = ""
	ReasonPermanent Reason = "permanent"
	ReasonThrottled Reason = "throttled"
	ReasonExhausted Reason = "exhausted"
	ReasonBudget    Reason = "budget"
	ReasonExpired   Reason = "expired"
)

// Rejection reasons, distinguishable and reported in a fixed order.
var (
	ErrInvalidArgument = errors.New("notify: invalid argument")
	ErrTaskNotFound    = errors.New("notify: task not found")
	ErrTaskFinished    = errors.New("notify: task already finished")
	ErrClockRegression = errors.New("notify: clock regression")
	ErrTooEarly        = errors.New("notify: attempt too early")
	ErrDuplicateTask   = errors.New("notify: duplicate task id")
)

// Jitter is a deterministic jitter function injected into the scheduler.
// It is called as jitter(id, n, d) and must return the same value for the
// same arguments across replays.
type Jitter func(id string, n, d int64) int64

// Outcome is the result of an accepted Fail call.
type Outcome struct {
	Channel string // channel of the next attempt (empty when dead)
	NextAt  int64  // earliest time of the next attempt
	Dead    bool   // true when the task entered the dead-letter state
	Reason  Reason // dead-letter reason (ReasonNone when not dead)
}

const (
	maxCreated = int64(1_000_000_000_000) // 1e12
	maxNow     = int64(1_000_000_000_000) // 1e12
	maxTTL     = int64(1_000_000_000)     // 1e9
	maxRA      = int64(1_000_000_000)     // 1e9
)

type task struct {
	chain    []string
	cur      int
	n        int64
	att      int64
	nextAt   int64
	deadline int64
	done     bool
	reason   Reason // ReasonNone means completed by Success
}

// Scheduler is safe for concurrent use; results are equivalent to some
// serial order of the operations.
type Scheduler struct {
	mu        sync.Mutex
	base      int64
	cap       int64
	m         int64
	a         int64
	raCap     int64
	k         int64
	cool      int64
	jitter    Jitter
	tasks     map[string]*task
	gfail     map[string]int64
	openUntil map[string]int64
	maxNow    int64
}

// NewScheduler validates the construction parameters and returns a
// scheduler, or ErrInvalidArgument.
func NewScheduler(base, capVal, m, a, raCap, k, cool int64, jitter Jitter) (*Scheduler, error) {
	if base < 1 || base > 1_000_000 {
		return nil, ErrInvalidArgument
	}
	if capVal < base || capVal > 1_000_000_000 {
		return nil, ErrInvalidArgument
	}
	if m < 1 || m > 16 {
		return nil, ErrInvalidArgument
	}
	if a < 1 || a > 100 {
		return nil, ErrInvalidArgument
	}
	if raCap < 0 || raCap > 1_000_000_000 {
		return nil, ErrInvalidArgument
	}
	if k < 1 || k > 1000 {
		return nil, ErrInvalidArgument
	}
	if cool < 1 || cool > 1_000_000_000 {
		return nil, ErrInvalidArgument
	}
	if jitter == nil {
		return nil, ErrInvalidArgument
	}
	return &Scheduler{
		base:      base,
		cap:       capVal,
		m:         m,
		a:         a,
		raCap:     raCap,
		k:         k,
		cool:      cool,
		jitter:    jitter,
		tasks:     make(map[string]*task),
		gfail:     make(map[string]int64),
		openUntil: make(map[string]int64),
	}, nil
}

// Submit registers a task. It returns ErrInvalidArgument for invalid
// parameters and ErrDuplicateTask for an already registered id.
func (s *Scheduler) Submit(id string, chain []string, created, ttl int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if id == "" || len(chain) < 1 || len(chain) > 4 ||
		created < 0 || created > maxCreated || ttl < 1 || ttl > maxTTL {
		return ErrInvalidArgument
	}
	seen := make(map[string]bool, len(chain))
	for _, ch := range chain {
		if ch == "" || seen[ch] {
			return ErrInvalidArgument
		}
		seen[ch] = true
	}
	if _, ok := s.tasks[id]; ok {
		return ErrDuplicateTask
	}
	s.tasks[id] = &task{
		chain:    append([]string(nil), chain...),
		nextAt:   created,
		deadline: created + ttl,
	}
	return nil
}

// Fail reports a failed attempt. On success it returns the next attempt
// schedule or the dead-letter outcome.
func (s *Scheduler) Fail(id string, now int64, class Class, ra int64) (Outcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if now < 0 || now > maxNow || ra < 0 || ra > maxRA ||
		(class != ClassTransient && class != ClassThrottled && class != ClassPermanent) ||
		(class != ClassThrottled && ra != 0) {
		return Outcome{}, ErrInvalidArgument
	}
	t, ok := s.tasks[id]
	if !ok {
		return Outcome{}, ErrTaskNotFound
	}
	if t.done {
		return Outcome{}, ErrTaskFinished
	}
	if now < s.maxNow {
		return Outcome{}, ErrClockRegression
	}
	if now < t.nextAt {
		return Outcome{}, ErrTooEarly
	}
	s.maxNow = now

	// (1) count the failure against the total budget.
	t.att++
	ch := t.chain[t.cur]

	// (2) per-channel global failure streak and circuit breaking.
	if class != ClassPermanent {
		s.gfail[ch]++
		if s.gfail[ch] >= s.k {
			s.openUntil[ch] = now + s.cool
		}
	}

	// (3) decide between same-channel backoff and the switch branch.
	reason := ReasonNone
	var w int64
	switching := false
	switch {
	case class == ClassPermanent:
		switching, reason = true, ReasonPermanent
	case class == ClassThrottled && ra > s.raCap:
		switching, reason = true, ReasonThrottled
	default:
		t.n++
		if t.n >= s.m {
			switching, reason = true, ReasonExhausted
		} else {
			d := s.base << (t.n - 1)
			if d > s.cap {
				d = s.cap
			}
			j := s.jitter(id, t.n, d)
			if j < 0 {
				j = 0
			}
			if j > d/4 {
				j = d / 4
			}
			w = d - j
			if ra > w {
				w = ra
			}
		}
	}

	// (4) switch branch: pick the lowest-indexed later channel that is
	// not circuit-broken at now.
	if switching {
		j := -1
		for i := t.cur + 1; i < len(t.chain); i++ {
			if s.openUntil[t.chain[i]] <= now {
				j = i
				break
			}
		}
		if j < 0 {
			t.done = true
			t.reason = reason
			return Outcome{Dead: true, Reason: reason}, nil
		}
		t.cur = j
		t.n = 0
		w = 0
	}

	// (5) total failure budget.
	if t.att >= s.a {
		t.done = true
		t.reason = ReasonBudget
		return Outcome{Dead: true, Reason: ReasonBudget}, nil
	}

	// (6) schedule the next attempt; the deadline is inclusive.
	t.nextAt = now + w
	if t.nextAt > t.deadline {
		t.done = true
		t.reason = ReasonExpired
		return Outcome{Dead: true, Reason: ReasonExpired}, nil
	}
	return Outcome{Channel: t.chain[t.cur], NextAt: t.nextAt}, nil
}

// Success marks the task completed and clears gfail of its current channel.
func (s *Scheduler) Success(id string, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if now < 0 || now > maxNow {
		return ErrInvalidArgument
	}
	t, ok := s.tasks[id]
	if !ok {
		return ErrTaskNotFound
	}
	if t.done {
		return ErrTaskFinished
	}
	if now < s.maxNow {
		return ErrClockRegression
	}
	if now < t.nextAt {
		return ErrTooEarly
	}
	s.maxNow = now
	t.done = true
	s.gfail[t.chain[t.cur]] = 0
	return nil
}
