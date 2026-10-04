// Package guard combines a counting-window circuit breaker with a
// bulkhead of concurrency permits and a bounded wait queue. All
// operations are safe for concurrent use and behave as if executed in
// some serial order.
package guard

import (
	"errors"
	"fmt"
	"sync"

	"ontology/breaker"
	"ontology/bulkhead"
	"ontology/ring"
)

var (
	ErrInvalidParam    = errors.New("guard: invalid parameter")
	ErrInvalidTime     = errors.New("guard: invalid time")
	ErrClockRegression = errors.New("guard: clock regression")
	ErrNotInService    = errors.New("guard: id is not an in-service permit")
	ErrUnknownID       = errors.New("guard: unknown id")
)

// maxTime is the largest legal value of now.
const maxTime = int64(1_000_000_000_000_000)

// Config holds the guard construction parameters.
type Config struct {
	N  int   // ring window size
	M  int   // minimum calls before evaluation
	F  int   // failure-rate threshold, percent
	SR int   // slow-call-rate threshold, percent
	S  int64 // slow-call duration, ms
	O  int64 // open duration, ms
	Wt int64 // queue wait timeout, ms
	H  int   // half-open probe count
	C  int   // concurrency permits
	Q  int   // queue capacity
}

// Validate checks the parameter bounds.
func (c Config) Validate() error {
	switch {
	case c.M < 1 || c.M > c.N || c.N > 1000:
		return fmt.Errorf("guard: require 1<=M<=N<=1000, got M=%d N=%d", c.M, c.N)
	case c.F < 1 || c.F > 100:
		return fmt.Errorf("guard: require 1<=F<=100, got F=%d", c.F)
	case c.SR < 1 || c.SR > 100:
		return fmt.Errorf("guard: require 1<=SR<=100, got SR=%d", c.SR)
	case c.S < 1 || c.S > 1_000_000_000:
		return fmt.Errorf("guard: require 1<=S<=1e9, got S=%d", c.S)
	case c.O < 1 || c.O > 1_000_000_000:
		return fmt.Errorf("guard: require 1<=O<=1e9, got O=%d", c.O)
	case c.Wt < 1 || c.Wt > 1_000_000_000:
		return fmt.Errorf("guard: 1<=Wt<=1e9, got Wt=%d", c.Wt)
	case c.H < 1 || c.H > 100:
		return fmt.Errorf("guard: require 1<=H<=100, got H=%d", c.H)
	case c.C < 1 || c.C > 1000:
		return fmt.Errorf("guard: require 1<=C<=1000, got C=%d", c.C)
	case c.Q < 0 || c.Q > 1000:
		return fmt.Errorf("guard: require 0<=Q<=1000, got Q=%d", c.Q)
	}
	return nil
}

// AcquireResult is the outcome of an Acquire call.
type AcquireResult int

const (
	Granted AcquireResult = iota
	Queued
	RejectedOpen
	RejectedHalfOpenFull
	RejectedFull
)

func (r AcquireResult) String() string {
	switch r {
	case Granted:
		return "放行(Granted)"
	case Queued:
		return "排队(Queued)"
	case RejectedOpen:
		return "拒绝(Open)"
	case RejectedHalfOpenFull:
		return "拒绝(HalfOpenFull)"
	case RejectedFull:
		return "拒绝(Full)"
	}
	return "未知"
}

// Snapshot reports the externally visible guard state.
type Snapshot struct {
	State     breaker.State
	Epoch     uint64
	RingCount int
	Failures  int
	Slows     int
	InService int
	QueueLen  int
}

// Guard is the downstream call guard.
type Guard struct {
	mu  sync.Mutex
	cfg Config
	br  *breaker.Breaker
	rg  *ring.Ring
	bh  *bulkhead.Bulkhead

	maxNow int64 // largest now that passed validation
}

// New builds a guard from a validated config.
func New(cfg Config) (*Guard, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Guard{
		cfg: cfg,
		br:  breaker.New(cfg.M, cfg.F, cfg.SR, cfg.H, cfg.O),
		rg:  ring.New(cfg.N),
		bh:  bulkhead.New(cfg.C, cfg.Q),
	}, nil
}

// Acquire decides whether a call proceeds, queues or is rejected.
func (g *Guard) Acquire(now int64) (AcquireResult, int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.checkTime(now); err != nil {
		return 0, 0, err
	}
	g.settle(now)
	g.maxNow = now
	switch g.br.State() {
	case breaker.Open:
		return RejectedOpen, 0, nil
	case breaker.HalfOpen:
		if !g.br.ProbeAllowed() {
			return RejectedHalfOpenFull, 0, nil
		}
	}
	if id, ok := g.bh.Grant(g.br.Epoch()); ok {
		if g.br.State() == breaker.HalfOpen {
			g.br.NoteProbe()
		}
		return Granted, id, nil
	}
	if g.br.State() == breaker.Closed {
		if id, ok := g.bh.Enqueue(now); ok {
			return Queued, id, nil
		}
	}
	return RejectedFull, 0, nil
}

// Release returns the permit held by id and records its outcome.
func (g *Guard) Release(id int, ok bool, dur int64, now int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if id <= 0 || dur < 0 {
		return ErrInvalidParam
	}
	if err := g.checkTime(now); err != nil {
		return err
	}
	if !g.bh.IsInService(id) {
		return ErrNotInService
	}
	g.settle(now)
	g.maxNow = now
	epoch, _ := g.bh.Release(id)
	if epoch == g.br.Epoch() {
		fail := !ok
		slow := dur >= g.cfg.S
		switch g.br.State() {
		case breaker.Closed:
			g.rg.Add(fail, slow)
			if g.br.EvalWindow(g.rg.Count(), g.rg.Failures(), g.rg.Slows(), now) {
				g.rg.Clear()
				g.bh.RevokeAll()
			}
		case breaker.HalfOpen:
			g.br.EvalProbe(fail, slow, now)
			if g.br.State() != breaker.HalfOpen {
				g.rg.Clear()
			}
		}
	}
	if g.br.State() == breaker.Closed {
		g.bh.DrainQueue(g.br.Epoch())
	}
	return nil
}

// Status reports the lifecycle state of id after settling.
func (g *Guard) Status(id int, now int64) (bulkhead.Outcome, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if id <= 0 {
		return 0, ErrInvalidParam
	}
	if err := g.checkTime(now); err != nil {
		return 0, err
	}
	if !g.bh.Known(id) {
		return 0, ErrUnknownID
	}
	g.settle(now)
	g.maxNow = now
	outcome, _ := g.bh.Status(id)
	return outcome, nil
}

// Snapshot returns the current guard state without settling.
func (g *Guard) Snapshot() Snapshot {
	g.mu.Lock()
	defer g.mu.Unlock()
	return Snapshot{
		State:     g.br.State(),
		Epoch:     g.br.Epoch(),
		RingCount: g.rg.Count(),
		Failures:  g.rg.Failures(),
		Slows:     g.rg.Slows(),
		InService: g.bh.InService(),
		QueueLen:  g.bh.QueueLen(),
	}
}

// Ledger returns the number of issued ids and the per-outcome tallies,
// whose sum always equals the issued count.
func (g *Guard) Ledger() (issued int, counts [5]int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.bh.Issued(), g.bh.Counts()
}

// settle advances time-based bookkeeping: queue timeouts first, then the
// Open to HalfOpen promotion.
func (g *Guard) settle(now int64) {
	g.bh.SettleTimeouts(now, g.cfg.Wt)
	g.br.Settle(now)
}

func (g *Guard) checkTime(now int64) error {
	if now < 0 || now > maxTime {
		return ErrInvalidTime
	}
	if now < g.maxNow {
		return ErrClockRegression
	}
	return nil
}
