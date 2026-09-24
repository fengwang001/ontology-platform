// Package sampler periodically samples call stacks into a tree.
package sampler

import (
	"sync/atomic"
	"time"

	"ontology/stack"
	"ontology/tree"
)

// Config wires a sampler to injectable dependencies.
type Config struct {
	Interval time.Duration   // fixed sampling interval
	MaxDepth int             // stack depth limit, <=0 means default
	Clock    func() int64    // monotonic nanoseconds
	Source   func() []string // stack provider, root first
	Busy     func() bool     // injected busy window: drop the sample
}

// Stats is a point-in-time read of sampler counters.
type Stats struct {
	Expected  int64 // ticks where a sample was due
	Dropped   int64 // samples dropped in busy windows
	Invalid   int64 // rejected stacks (empty / source error)
	Anomalous int64 // ticks skipped for clock rollback or huge jumps
}

// Sampler drives one stack per Tick. Tick is called from a single
// goroutine; Stats and Stop are safe to call from others.
type Sampler struct {
	cfg       Config
	tree      *tree.Tree
	last      int64
	started   bool
	expected  atomic.Int64
	dropped   atomic.Int64
	invalid   atomic.Int64
	anomalous atomic.Int64
	stopped   atomic.Bool
}

// New returns a Sampler inserting into t.
func New(cfg Config, t *tree.Tree) *Sampler {
	if cfg.Interval <= 0 {
		cfg.Interval = 10 * time.Millisecond
	}
	return &Sampler{cfg: cfg, tree: t}
}

// Tick performs one sampling step. Identity C is maintained:
// tree.Samples() + Dropped + Invalid == Expected.
func (s *Sampler) Tick() {
	if s.stopped.Load() {
		return
	}
	now := s.cfg.Clock()
	if s.started {
		delta := now - s.last
		if delta < 0 || delta > int64(10*s.cfg.Interval) {
			s.anomalous.Add(1) // clock rollback or huge jump: skip
			s.last = now
			return
		}
	}
	s.last, s.started = now, true
	s.expected.Add(1)
	if s.cfg.Busy != nil && s.cfg.Busy() {
		s.dropped.Add(1)
		return
	}
	st, err := stack.Normalize(s.cfg.Source(), s.cfg.MaxDepth)
	if err != nil {
		s.invalid.Add(1)
		return
	}
	s.tree.Insert(st)
}

// Stats returns a consistent snapshot of the counters.
func (s *Sampler) Stats() Stats {
	return Stats{
		Expected:  s.expected.Load(),
		Dropped:   s.dropped.Load(),
		Invalid:   s.invalid.Load(),
		Anomalous: s.anomalous.Load(),
	}
}

// Stop is idempotent; Tick becomes a no-op afterwards.
func (s *Sampler) Stop() { s.stopped.Store(true) }

// Stopped reports whether Stop was called.
func (s *Sampler) Stopped() bool { return s.stopped.Load() }
