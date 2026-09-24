package batcher

import (
	"time"

	"ontology/req"
)

type Config struct {
	MaxCount int
	MaxBytes int
	MaxWait  time.Duration
}

type Clock interface {
	Now() time.Time
	Timer(time.Duration) (<-chan time.Time, func())
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

func (wallClock) Timer(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTimer(d)
	return t.C, func() { t.Stop() }
}

type fakeTimer struct {
	ch       chan time.Time
	deadline time.Time
}

type FakeClock struct {
	mu      chan struct{}
	now     time.Time
	pending map[*fakeTimer]struct{}
}

func NewFakeClock() *FakeClock {
	c := &FakeClock{
		mu:      make(chan struct{}, 1),
		now:     time.Unix(0, 0),
		pending: make(map[*fakeTimer]struct{}),
	}
	c.mu <- struct{}{}
	return c
}

func (c *FakeClock) Now() time.Time {
	<-c.mu
	now := c.now
	c.mu <- struct{}{}
	return now
}

func (c *FakeClock) Timer(d time.Duration) (<-chan time.Time, func()) {
	<-c.mu
	t := &fakeTimer{ch: make(chan time.Time, 1), deadline: c.now.Add(d)}
	if d <= 0 || !c.now.Before(t.deadline) {
		t.ch <- c.now
		c.mu <- struct{}{}
		return t.ch, func() {}
	}
	c.pending[t] = struct{}{}
	c.mu <- struct{}{}
	return t.ch, func() {
		<-c.mu
		delete(c.pending, t)
		c.mu <- struct{}{}
	}
}

func (c *FakeClock) Advance(d time.Duration) {
	<-c.mu
	c.now = c.now.Add(d)
	for t := range c.pending {
		if !c.now.Before(t.deadline) {
			select {
			case t.ch <- c.now:
			default:
			}
			delete(c.pending, t)
		}
	}
	c.mu <- struct{}{}
}

type Batch struct {
	Requests []*req.Request
}

type Batcher struct {
	cfg     Config
	clock   Clock
	requests chan *req.Request
	batches chan Batch
	closeIn chan struct{}
	closed  chan struct{}
	addMu   chan struct{}
}

func New(cfg Config, clock Clock) *Batcher {
	if clock == nil {
		clock = wallClock{}
	}
	b := &Batcher{
		cfg:      cfg,
		clock:    clock,
		requests: make(chan *req.Request, 1024),
		batches:  make(chan Batch, 16),
		closeIn:  make(chan struct{}),
		closed:   make(chan struct{}),
		addMu:    make(chan struct{}, 1),
	}
	b.addMu <- struct{}{}
	go b.run()
	return b
}

func (b *Batcher) Add(r *req.Request) error {
	<-b.addMu
	select {
	case <-b.closeIn:
		b.addMu <- struct{}{}
		return req.ErrClosed
	default:
	}
	b.requests <- r
	b.addMu <- struct{}{}
	return nil
}

func (b *Batcher) Batches() <-chan Batch { return b.batches }

func (b *Batcher) Close() {
	<-b.addMu
	select {
	case <-b.closeIn:
	default:
		close(b.closeIn)
	}
	b.addMu <- struct{}{}
	<-b.closed
}

func (b *Batcher) run() {
	defer close(b.closed)
	var current []*req.Request
	var bytes int
	var wait <-chan time.Time
	var stopTimer func()
	clearTimer := func() {
		if stopTimer != nil {
			stopTimer()
			wait, stopTimer = nil, nil
		}
	}
	flush := func() {
		if len(current) == 0 {
			return
		}
		b.batches <- Batch{Requests: current}
		current, bytes = nil, 0
		clearTimer()
	}
	for {
		select {
		case r := <-b.requests:
			if len(current) == 0 {
				wait, stopTimer = b.clock.Timer(b.cfg.MaxWait)
			}
			if len(current) > 0 && bytes+len(r.Payload) > b.cfg.MaxBytes {
				flush()
			}
			current = append(current, r)
			bytes += len(r.Payload)
			if len(current) >= b.cfg.MaxCount || bytes >= b.cfg.MaxBytes {
				flush()
			}
		case <-wait:
			flush()
		case <-b.closeIn:
			for {
				select {
				case r := <-b.requests:
					current = append(current, r)
				default:
					flush()
					close(b.batches)
					return
				}
			}
		}
	}
}
