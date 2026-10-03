// Package sampler provides a tail sampler with decision cache and quota.
package sampler

import (
	"errors"
	"hash/fnv"
	"sync"

	"ontology/policy"
)

// Reason values mirror policy.Reason* plus the quota drop.
const (
	ReasonError      = policy.ReasonError
	ReasonLatency    = policy.ReasonLatency
	ReasonProb       = policy.ReasonProb
	ReasonSampledOut = policy.ReasonSampledOut
	ReasonBudget     = "Budget"
)

var (
	errInvalidArgs   = errors.New("sampler: construction parameter out of range")
	errInvalidInput  = errors.New("sampler: invalid ingest argument")
	errInvalidTick   = errors.New("sampler: invalid tick time")
	errClockRewind   = errors.New("sampler: clock moved backwards")
	errDuplicateSpan = errors.New("sampler: duplicate spanID in buffered trace")
)

// Decision is one trace-level verdict in emission order.
type Decision struct {
	TraceID string
	Keep    bool
	Reason  string
	Spans   int64
	At      int64
	Evicted bool
}

// Config carries all construction parameters in documented order.
type Config struct {
	W, Td, Sc, Nmax, Cmax, L, Wb, Q, P int64
	H                                  func(string) uint32
}

// Stats exposes the four exit counters and the Tick inspection counter.
type Stats struct {
	Decisions, LateKept, LateDropped int64
	TickInspected, TickDecided       int64
}

// Sampler buffers out-of-order spans and emits trace-level decisions.
type Sampler struct {
	mu sync.Mutex

	pol   *policy.Policy
	dec   *decisionCache
	w, sc int64
	nmax  int64
	wb, q int64

	buf   map[string]*bufferNode
	order bufferHeap

	window int64
	used   int64
	clock  int64

	lateKept    int64
	lateDropped int64
	decisions   int64

	// tickInspected counts buffered heap tops examined by Tick; for one Tick
	// call it is at most tickDecided+1.
	tickInspected int64
	tickDecided   int64
}

// New validates ranges and constructs the sampler; out-of-range means failure.
func New(cfg Config) (*Sampler, error) {
	switch {
	case cfg.W < 0 || cfg.W > 1e9,
		cfg.Td < 0 || cfg.Td > 1e9,
		cfg.Sc < 1 || cfg.Sc > 1e4,
		cfg.Nmax < 1 || cfg.Nmax > 1e5,
		cfg.Cmax < 0 || cfg.Cmax > 1e6,
		cfg.L < 0 || cfg.L > 1e9,
		cfg.Wb < 1 || cfg.Wb > 1e9,
		cfg.Q < 0 || cfg.Q > 1e9,
		cfg.P < 0 || cfg.P > 10000:
		return nil, errInvalidArgs
	}
	hash := cfg.H
	if hash == nil {
		hash = fnvHash
	}
	s := &Sampler{
		pol:  policy.New(cfg.L, cfg.P, hash),
		dec:  newDecisionCache(cfg.Td, cfg.Cmax),
		w:    cfg.W,
		sc:   cfg.Sc,
		nmax: cfg.Nmax,
		wb:   cfg.Wb,
		q:    cfg.Q,
		buf:  make(map[string]*bufferNode),
	}
	return s, nil
}

// Stats returns cumulative counters.
func (s *Sampler) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Stats{
		Decisions:     s.decisions,
		LateKept:      s.lateKept,
		LateDropped:   s.lateDropped,
		TickInspected: s.tickInspected,
		TickDecided:   s.tickDecided,
	}
}

func fnvHash(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}
