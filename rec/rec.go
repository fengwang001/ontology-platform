// Package rec adds crash recovery, replay and atomic snapshots on top of ckp.
package rec

import (
	"sync"

	"ontology/ckp"
)

// Ckpt is re-exported so callers only need this package.
type Ckpt = ckp.Ckpt

// Sentinel errors, re-exported from ckp.
var (
	ErrNonPositive  = ckp.ErrNonPositive
	ErrGap          = ckp.ErrGap
	ErrNoCheckpoint = ckp.ErrNoCheckpoint
)

// Snapshot is an atomic view of the stream state (the four-tuple).
type Snapshot struct {
	Total   int64
	Applied int64
	Flushed int64
	Ckpt    Ckpt
}

// Processor applies events, flushes, checkpoints and restarts. Goroutine-safe.
type Processor struct {
	mu        sync.Mutex
	st        ckp.State
	flushScan int64 // positions re-accumulated by the last Flush; incremental => 0
}

func New() *Processor { return &Processor{} }

// Apply applies one event; pos must be contiguous. Rejection changes nothing.
func (p *Processor) Apply(pos, delta int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.st.Apply(pos, delta)
}

// Flush marks applied effects durable. flushTotal is taken from the running
// total (incremental), so the number of re-accumulated positions is 0.
func (p *Processor) Flush() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.flushScan = 0
	p.st.Flush()
}

// Checkpoint persists (flushed, flushTotal) when flushed advanced; else no-op.
func (p *Processor) Checkpoint() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.st.Checkpoint()
}

// Restart simulates crash recovery: resets memory to the persistent
// checkpoint and returns it. Fails with ErrNoCheckpoint when none exists.
func (p *Processor) Restart() (Ckpt, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.st.Restart()
}

// Snapshot atomically returns the four-tuple (total, applied, flushed, ckpt).
func (p *Processor) Snapshot() Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return Snapshot{
		Total:   p.st.Total(),
		Applied: p.st.Applied(),
		Flushed: p.st.Flushed(),
		Ckpt:    p.st.Ckpt(),
	}
}
