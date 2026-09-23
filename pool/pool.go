// Package pool leases expensive resources: generational handles, FIFO
// handoff with commit-at-dequeue, fault recovery and idle eviction.
package pool

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"ontology/evict"
	"ontology/handle"
	"ontology/slots"
	"ontology/waitq"
)

var (
	ErrClosed         = errors.New("pool: closed")
	ErrStaleHandle    = errors.New("pool: stale handle")
	ErrTooManyWaiters = errors.New("pool: too many waiters")
	ErrCreate         = errors.New("pool: create failed")
)

type Factory struct {
	Create   func(ctx context.Context) (any, error)
	Validate func(res any) bool
	Destroy  func(res any) error
}
type Config struct {
	Max, MinIdle, MaxWaiters int
	MaxIdleTime              time.Duration
	Now                      func() time.Time
}
type Stats struct{ Idle, Lent, Creating, Destroying, Waiters int }

const dResource, dPermit, dErr = 0, 1, 2

type delivery struct {
	kind, slot int
	gen        uint64
	res        any
	err        error
}
type idleEnt struct {
	slot int
	res  any
	at   time.Time
}
type Pool struct {
	mu     sync.Mutex
	f      Factory
	cfg    Config
	led    *slots.Ledger
	pol    evict.Policy
	q      waitq.Queue[delivery]
	idle   []idleEnt // ordered by return time, newest last
	lent   map[int]any
	closed bool
}

func New(cfg Config, f Factory) *Pool {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Pool{f: f, cfg: cfg, led: slots.New(cfg.Max),
		pol: evict.Policy{MaxIdle: cfg.MaxIdleTime, MinIdle: cfg.MinIdle}, lent: map[int]any{}}
}

// Acquire leases a resource. Dequeue is the commit point: a waiter whose
// ctx fires after dequeue must accept the (already buffered) delivery.
func (p *Pool) Acquire(ctx context.Context) (res any, h handle.Handle, err error) {
	permit := -1
	for {
		p.mu.Lock()
		if p.closed {
			if permit >= 0 { p.led.AbortCreate(permit) }
			p.mu.Unlock()
			return nil, handle.Handle{}, ErrClosed
		}
		if act := permit >= 0 || p.q.Len() == 0; act {
			if n := len(p.idle); n > 0 { // LIFO reuse: newest first
				e := p.idle[n-1]
				p.idle = p.idle[:n-1]
				if permit >= 0 { p.led.AbortCreate(permit); permit = -1 }
				gen := p.led.MarkLent(e.slot)
				p.lent[e.slot] = e.res
				p.mu.Unlock()
				if p.f.Validate(e.res) { return e.res, handle.Handle{e.slot, gen}, nil }
				p.destroy(e.slot, e.res)
				continue
			}
			if permit < 0 && p.led.CanCreate() { permit = p.led.BeginCreate() }
			if permit >= 0 {
				slot := permit
				permit = -1
				p.mu.Unlock()
				res, err := p.create(ctx) // waiter's own goroutine creates
				p.mu.Lock()
				if err != nil || p.closed {
					p.led.AbortCreate(slot)
					if !p.closed { p.passPermitLocked() } // chain the permit
					p.mu.Unlock()
					if err == nil { p.f.Destroy(res); err = ErrClosed }
					return nil, handle.Handle{}, err
				}
				gen := p.led.MarkLent(slot)
				p.lent[slot] = res
				p.mu.Unlock()
				return res, handle.Handle{slot, gen}, nil
			}
		}
		if p.cfg.MaxWaiters > 0 && p.q.Len() >= p.cfg.MaxWaiters {
			p.mu.Unlock()
			return nil, handle.Handle{}, ErrTooManyWaiters
		}
		w := waitq.NewWaiter[delivery]()
		p.q.Enqueue(w)
		p.mu.Unlock()
		var d delivery
		select {
		case d = <-w.Ch:
		case <-ctx.Done():
			p.mu.Lock()
			queued := p.q.Cancel(w)
			p.mu.Unlock()
			if queued { return nil, handle.Handle{}, ctx.Err() }
			d = <-w.Ch
		}
		if d.kind == dResource { return d.res, handle.Handle{d.slot, d.gen}, nil }
		if d.kind == dErr { return nil, handle.Handle{}, d.err }
		if ctx.Err() != nil {
			p.mu.Lock()
			p.led.AbortCreate(d.slot)
			if !p.closed { p.passPermitLocked() }
			p.mu.Unlock()
			return nil, handle.Handle{}, ctx.Err()
		}
		permit = d.slot
	}
}

func (p *Pool) Release(h handle.Handle) error {
	p.mu.Lock()
	if !p.led.IsLent(h.Slot, h.Gen) { p.mu.Unlock(); return ErrStaleHandle }
	res := p.lent[h.Slot]
	if p.closed { p.mu.Unlock(); p.destroy(h.Slot, res); return nil }
	if w, ok := p.q.Dequeue(); ok { // direct FIFO handoff, never via idle
		w.Ch <- delivery{kind: dResource, slot: h.Slot, gen: p.led.MarkLent(h.Slot), res: res}
		p.mu.Unlock()
		return nil
	}
	delete(p.lent, h.Slot)
	p.led.MarkIdle(h.Slot)
	p.idle = append(p.idle, idleEnt{h.Slot, res, p.cfg.Now()})
	p.mu.Unlock()
	return nil
}

func (p *Pool) Discard(h handle.Handle) error {
	p.mu.Lock()
	if !p.led.IsLent(h.Slot, h.Gen) { p.mu.Unlock(); return ErrStaleHandle }
	res := p.lent[h.Slot]
	p.mu.Unlock()
	p.destroy(h.Slot, res)
	p.mu.Lock()
	if !p.closed && p.q.Len() > 0 { p.passPermitLocked() }
	p.mu.Unlock()
	return nil
}

func (p *Pool) Evict() {
	p.mu.Lock()
	ats := make([]time.Time, len(p.idle))
	for i, e := range p.idle { ats[i] = e.at }
	n := p.pol.Select(ats, p.cfg.Now())
	victims := append([]idleEnt(nil), p.idle[:n]...)
	p.idle = append(p.idle[:0], p.idle[n:]...)
	p.mu.Unlock()
	for _, e := range victims { p.destroy(e.slot, e.res) }
}

func (p *Pool) Close() {
	p.mu.Lock()
	if p.closed { p.mu.Unlock(); return }
	p.closed = true
	for {
		w, ok := p.q.Dequeue()
		if !ok { break }
		w.Ch <- delivery{kind: dErr, err: ErrClosed}
	}
	victims := p.idle
	p.idle = nil
	p.mu.Unlock()
	for _, e := range victims { p.destroy(e.slot, e.res) }
}

func (p *Pool) Stats() Stats {
	p.mu.Lock()
	defer p.mu.Unlock()
	i, l, c, d := p.led.Counts()
	return Stats{i, l, c, d, p.q.Len()}
}
func (p *Pool) Check() error { p.mu.Lock(); defer p.mu.Unlock(); return p.led.Check() }

// passPermitLocked reserves a Creating slot and hands it to the front
// waiter, who creates in its own goroutine; a no-op when the queue is empty.
func (p *Pool) passPermitLocked() {
	if w, ok := p.q.Dequeue(); ok {
		w.Ch <- delivery{kind: dPermit, slot: p.led.BeginCreate()}
	}
}

func (p *Pool) destroy(slot int, res any) {
	p.mu.Lock()
	delete(p.lent, slot)
	p.led.BeginDestroy(slot)
	p.mu.Unlock()
	p.f.Destroy(res) // an error here still frees the slot below
	p.mu.Lock()
	p.led.Destroyed(slot)
	p.mu.Unlock()
}

func (p *Pool) create(ctx context.Context) (res any, err error) {
	defer func() {
		if r := recover(); r != nil { err = fmt.Errorf("%w: panic: %v", ErrCreate, r) }
	}()
	res, err = p.f.Create(ctx)
	if err != nil { err = fmt.Errorf("%w: %w", ErrCreate, err) }
	return res, err
}
