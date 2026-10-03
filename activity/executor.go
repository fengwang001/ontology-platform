package activity

import (
	"sync"

	"ontology/deadline"
	"ontology/retry"
)

type Logger interface {
	Printf(format string, args ...any)
}

type Executor struct {
	mu     sync.Mutex
	acts   map[string]*act
	clock  int64
	log    Logger
	probes int
}

// NewExecutor 创建执行器，全局时钟初值 0。
func NewExecutor() *Executor {
	return &Executor{acts: map[string]*act{}}
}

func (e *Executor) SetLogger(l Logger) { e.log = l }

func (e *Executor) logf(f string, args ...any) {
	if e.log != nil {
		e.log.Printf(f, args...)
	}
}

// Schedule 创建活动：t0=now，k=1，Scheduled。
func (e *Executor) Schedule(id []byte, s2s, s2c, hb, sc int64, m int, d0, cap int64, now int64) error {
	if len(id) == 0 || now < 0 || now > 1_000_000_000_000_000 {
		return ErrArgument
	}
	cfg, err := deadline.NewConfig(s2s, s2c, hb, sc)
	if err != nil {
		return ErrConfig
	}
	pol, err := retry.New(m, d0, cap)
	if err != nil {
		return ErrConfig
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if now < e.clock {
		return ErrClock
	}
	key := string(id)
	if _, ok := e.acts[key]; ok {
		return ErrExists
	}
	a := &act{
		id:    append([]byte(nil), id...),
		cfg:   cfg,
		pol:   pol,
		t0:    now,
		g:     now,
		k:     1,
		state: Scheduled,
	}
	a.rebuildScheduled()
	e.acts[key] = a
	e.clock = now
	e.logf("Schedule id=%q now=%d cfg=%+v M=%d d0=%d cap=%d -> Scheduled k=1", id, now, cfg, m, d0, cap)
	return nil
}

func (e *Executor) Start(id []byte, now int64) (attempt int, progress int64, err error) {
	a, err := e.prepareStart(id, now)
	if err != nil {
		e.logf("Start id=%q now=%d -> %v", id, now, err)
		return 0, 0, err
	}
	a.state = Running
	a.r = now
	a.h = now
	a.rebuildRunning()
	k, p := a.k, a.progress
	e.commit(a, now)
	e.logf("Start id=%q now=%d -> k=%d progress=%d", id, now, k, p)
	return k, p, nil
}

func (e *Executor) Heartbeat(id []byte, k int, now, progress int64) error {
	a, err := e.prepare(id, k, now)
	if err != nil {
		e.logf("Heartbeat id=%q k=%d now=%d p=%d -> %v", id, k, now, progress, err)
		return err
	}
	if a.state != Running {
		e.logf("Heartbeat id=%q k=%d now=%d -> %v (state=%d)", id, k, now, ErrState, a.state)
		return ErrState
	}
	a.h = now
	a.progress = progress
	a.rebuildRunning()
	e.commit(a, now)
	e.logf("Heartbeat id=%q k=%d now=%d p=%d -> ok", id, k, now, progress)
	return nil
}

func (e *Executor) Complete(id []byte, k int, now int64) error {
	a, err := e.prepare(id, k, now)
	if err != nil {
		e.logf("Complete id=%q k=%d now=%d -> %v", id, k, now, err)
		return err
	}
	if a.state != Running {
		return ErrState
	}
	a.terminalize(ReasonCompleted, now)
	e.commit(a, now)
	e.logf("Complete id=%q k=%d now=%d -> Completed", id, k, now)
	return nil
}

func (e *Executor) Fail(id []byte, k int, now int64, retryable bool) error {
	a, err := e.prepare(id, k, now)
	if err != nil {
		e.logf("Fail id=%q k=%d now=%d retry=%v -> %v", id, k, now, retryable, err)
		return err
	}
	if a.state != Running {
		return ErrState
	}
	if !retryable {
		a.terminalize(ReasonApp, now)
	} else {
		a.attemptFailed(ReasonApp, now)
	}
	e.commit(a, now)
	e.logf("Fail id=%q k=%d now=%d retry=%v -> state=%d attempt=%d reason=%q",
		id, k, now, retryable, a.state, a.k, a.reason)
	return nil
}

func (e *Executor) Status(id []byte, now int64) (Status, error) {
	if !validNow(now) {
		return Status{}, ErrArgument
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if now < e.clock {
		return Status{}, ErrClock
	}
	src, ok := e.acts[string(id)]
	if !ok {
		return Status{}, ErrNotFound
	}
	a := src.clone()
	e.probes = 0
	e.advance(a, now)
	st := Status{
		State:    a.state,
		Attempt:  a.k,
		Terminal: a.state == Terminal,
		Reason:   a.reason,
		Time:     a.termAt,
	}
	e.logf("Status id=%q now=%d -> %+v (probes=%d)", id, now, st, e.probes)
	return st, nil
}
