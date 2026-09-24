// Package batcher accumulates concurrent write requests into size/time-bounded batches.
package batcher

import (
	"errors"
	"sync"
	"time"

	"ontology/req"
	"ontology/wal"
)

// ErrClosed is returned by Add after Close has been called.
var ErrClosed = errors.New("batcher: closed")

// Clock allows injecting time for deterministic timeout-trigger tests.
type Clock interface {
	NewTimer(d time.Duration) Timer
}

// Timer is the subset of *time.Timer used by Batcher.
type Timer interface {
	C() <-chan time.Time
	Stop()
}

type realClock struct{}

func (realClock) NewTimer(d time.Duration) Timer { return &realTimer{time.NewTimer(d)} }

type realTimer struct{ t *time.Timer }

func (r *realTimer) C() <-chan time.Time { return r.t.C }
func (r *realTimer) Stop()               { r.t.Stop() }

// Batch is one finalized group of requests handed to the commit leader.
type Batch struct {
	Items []*req.Request
	// Bytes is the on-wire size this batch would occupy in the WAL.
	Bytes int
}

// Config sets the three accumulation triggers (first one reached fires).
type Config struct {
	MaxCount int           // flush once the batch holds this many requests
	MaxBytes int           // flush once projected wire bytes reach this size
	Wait     time.Duration // flush this long after the first request arrived
	Clock    Clock         // defaults to wall-clock timers when nil
}

// Batcher is safe for concurrent Add callers; a single internal loop forms batches.
type Batcher struct {
	cfg    Config
	in     chan *req.Request
	out    chan Batch
	done   chan struct{}
	stop   chan struct{}
	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
}

// New starts the accumulation loop. Batches must be consumed from Batches().
func New(cfg Config) *Batcher {
	if cfg.Clock == nil {
		cfg.Clock = realClock{}
	}
	b := &Batcher{
		cfg:  cfg,
		in:   make(chan *req.Request),
		out:  make(chan Batch),
		done: make(chan struct{}),
		stop: make(chan struct{}),
	}
	go b.loop()
	return b
}

// Add submits a request for accumulation. It returns ErrClosed once Close
// began; a request that was accepted is guaranteed to land in a batch.
func (b *Batcher) Add(r *req.Request) error {
	b.wg.Add(1)
	defer b.wg.Done()
	b.mu.Lock()
	closed := b.closed
	b.mu.Unlock()
	if closed {
		return ErrClosed
	}
	select {
	case b.in <- r:
		return nil
	case <-b.stop:
		return ErrClosed
	}
}

// Batches yields finalized batches until Close completes the final flush.
func (b *Batcher) Batches() <-chan Batch { return b.out }

// Close stops accepting requests, flushes the pending batch and shuts down.
func (b *Batcher) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	b.mu.Unlock()
	b.wg.Wait() // let in-flight Add calls finish enqueueing
	close(b.stop)
	<-b.done
}

func (b *Batcher) loop() {
	defer close(b.done)
	defer close(b.out)
	var items []*req.Request
	var bytes int
	var timer Timer
	stopTimer := func() {
		if timer != nil {
			timer.Stop()
			timer = nil
		}
	}
	flush := func() {
		if len(items) == 0 {
			return
		}
		b.out <- Batch{Items: items, Bytes: bytes + wal.BatchOverhead}
		items, bytes = nil, 0
		stopTimer()
	}
	for {
		var tch <-chan time.Time
		if timer != nil {
			tch = timer.C()
		}
	selectLoop:
		select {
		case r := <-b.in:
			if len(items) == 0 && b.cfg.Wait > 0 {
				timer = b.cfg.Clock.NewTimer(b.cfg.Wait)
			}
			items = append(items, r)
			bytes += wal.EntryOverhead + len(r.Payload)
			if len(items) >= b.cfg.MaxCount || bytes >= b.cfg.MaxBytes {
				flush()
			}
		case <-tch:
			flush()
		case <-b.stop:
			for { // drain requests accepted before Close
				select {
				case r := <-b.in:
					items = append(items, r)
					bytes += wal.EntryOverhead + len(r.Payload)
				default:
					flush()
					break selectLoop
				}
			}
		}
	}
}
