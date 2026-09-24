// Package batcher accumulates inbound items and flushes them as batches.
//
// A batch is flushed when any of three limits is reached first: item count,
// total bytes, or waiting time. A single item larger than the byte limit is
// never rejected: the pending batch (if any) is flushed and the oversized
// item is emitted as a singleton batch.
package batcher

import "time"

// Timer abstracts a time.Timer so the batcher can be driven deterministically.
type Timer interface {
	C() <-chan time.Time
	Stop() bool
}

// Clock creates timers. RealClock below wraps the standard library.
type Clock interface {
	NewTimer(d time.Duration) Timer
}

// RealClock uses wall-clock timers.
type RealClock struct{}

// NewTimer implements Clock.
func (RealClock) NewTimer(d time.Duration) Timer { return realTimer{time.NewTimer(d)} }

type realTimer struct{ t *time.Timer }

func (r realTimer) C() <-chan time.Time { return r.t.C }
func (r realTimer) Stop() bool          { return r.t.Stop() }

// Batcher is safe for concurrent Add calls. Batches are delivered on Batches.
type Batcher[T any] struct {
	in     chan T
	out    chan []T
	done   chan struct{}
	closed chan struct{}
}

// Config configures a Batcher.
type Config[T any] struct {
	MaxItems int
	MaxBytes int
	MaxWait  time.Duration
	Size     func(T) int
	Clock    Clock
}

// New starts a batcher with the given limits.
func New[T any](cfg Config[T]) *Batcher[T] {
	if cfg.MaxItems <= 0 {
		cfg.MaxItems = 1
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = 1
	}
	if cfg.Clock == nil {
		cfg.Clock = RealClock{}
	}
	b := &Batcher[T]{
		in:     make(chan T),
		out:    make(chan []T),
		done:   make(chan struct{}),
		closed: make(chan struct{}),
	}
	go b.loop(cfg)
	return b
}

// Add queues one item. It returns false if the batcher was already closed.
func (b *Batcher[T]) Add(v T) bool {
	select {
	case b.in <- v:
		return true
	case <-b.closed:
		return false
	}
}

// Batches returns the channel on which flushed batches are delivered.
func (b *Batcher[T]) Batches() <-chan []T { return b.out }

// Close stops accepting items and flushes the pending batch as the final one.
func (b *Batcher[T]) Close() {
	close(b.done)
	<-b.closed
}

func (b *Batcher[T]) loop(cfg Config[T]) {
	defer close(b.closed)
	defer close(b.out)

	var batch []T
	bytes := 0
	var timer Timer
	closing := false
	stopTimer := func() {
		if timer != nil {
			timer.Stop()
			timer = nil
		}
	}
	flush := func() {
		if len(batch) == 0 {
			return
		}
		stopTimer()
		b.out <- batch
		batch = nil
		bytes = 0
	}

	for {
		var fire <-chan time.Time
		if timer != nil {
			fire = timer.C()
		}
		select {
		case item := <-b.in:
			if closing {
				b.out <- []T{item}
				continue
			}
			n := cfg.Size(item)
			if n >= cfg.MaxBytes {
				flush()
				b.out <- []T{item}
				continue
			}
			if len(batch) == 0 && cfg.MaxWait > 0 {
				timer = cfg.Clock.NewTimer(cfg.MaxWait)
			}
			if len(batch) > 0 && bytes+n >= cfg.MaxBytes {
				flush()
				if cfg.MaxWait > 0 {
					timer = cfg.Clock.NewTimer(cfg.MaxWait)
				}
			}
			batch = append(batch, item)
			bytes += n
			if cfg.MaxWait == 0 || len(batch) >= cfg.MaxItems || bytes >= cfg.MaxBytes {
				flush()
			}
		case <-fire:
			flush()
		case <-b.done:
			closing = true
		}
		if closing {
			for {
				select {
				case item := <-b.in:
					batch = append(batch, item)
					bytes += cfg.Size(item)
				default:
					stopTimer()
					flush()
					return
				}
			}
		}
	}
}
