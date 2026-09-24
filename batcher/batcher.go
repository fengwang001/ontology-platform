// Package batcher accumulates concurrent writes into batches.
// A batch fires when the item cap, byte cap, or wait deadline is hit,
// whichever comes first.
package batcher

import (
	"time"

	"ontology/req"
)

// Reason identifies which trigger fired a batch.
type Reason int

const (
	ReasonCount Reason = iota
	ReasonBytes
	ReasonWait
	ReasonOversize // a single item larger than MaxBytes, flushed alone
)

// Clock is injectable so tests can advance time without sleeping.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// Config controls batching thresholds.
type Config struct {
	MaxCount int
	MaxBytes int
	MaxWait  time.Duration
	Clock    Clock
}

// Batcher is single-writer (the leader goroutine) and needs no locks.
type Batcher struct {
	cfg       Config
	items     []*req.Request
	bytes     int
	firstTime time.Time
}

// New builds a Batcher, filling defaults.
func New(cfg Config) *Batcher {
	if cfg.MaxCount <= 0 {
		cfg.MaxCount = 1
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = 1
	}
	if cfg.Clock == nil {
		cfg.Clock = realClock{}
	}
	return &Batcher{cfg: cfg}
}

// Add appends one request and reports whether the batch is ready to flush.
// A single item larger than MaxBytes is never rejected: it forms its own
// batch (possibly exceeding MaxBytes), unless items are already buffered,
// in which case the current batch fires first and the large item is held.
func (b *Batcher) Add(r *req.Request) (bool, Reason) {
	now := b.cfg.Clock.Now()
	size := len(r.Payload)
	if size > b.cfg.MaxBytes && len(b.items) == 0 {
		b.items = append(b.items, r)
		b.bytes = size
		return true, ReasonOversize
	}
	if len(b.items) == 0 {
		b.firstTime = now
	}
	b.items = append(b.items, r)
	b.bytes += size
	switch {
	case len(b.items) >= b.cfg.MaxCount:
		return true, ReasonCount
	case b.bytes >= b.cfg.MaxBytes:
		return true, ReasonBytes
	case b.cfg.MaxWait == 0:
		return true, ReasonWait
	case now.Sub(b.firstTime) >= b.cfg.MaxWait:
		return true, ReasonWait
	}
	return false, 0
}

// TimedOut reports whether the wait deadline elapsed with buffered items.
func (b *Batcher) TimedOut() bool {
	return len(b.items) > 0 && b.cfg.MaxWait > 0 &&
		b.cfg.Clock.Now().Sub(b.firstTime) >= b.cfg.MaxWait
}

// Pending reports buffered item count.
func (b *Batcher) Pending() int { return len(b.items) }

// Drain removes and returns all buffered items and resets the accumulator.
func (b *Batcher) Drain() []*req.Request {
	out := b.items
	b.items = nil
	b.bytes = 0
	b.firstTime = time.Time{}
	return out
}
