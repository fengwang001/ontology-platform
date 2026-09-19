package ontology

import (
	"sync"
	"sync/atomic"
)

// Option configures a subscription at creation time.
type Option func(*Subscription)

// WithOverflow sets the full-queue policy (default DropOldest).
func WithOverflow(p OverflowPolicy) Option {
	return func(s *Subscription) { s.policy = p }
}

// WithTail sets how buffered events are handled on unsubscribe/close
// (default DrainPending).
func WithTail(t TailPolicy) Option {
	return func(s *Subscription) { s.tail = t }
}

// WithAttributes restricts the subscription to the given attribute names.
// An empty list (the default) matches every attribute of the entity.
func WithAttributes(attrs ...string) Option {
	return func(s *Subscription) {
		s.attrs = make(map[string]struct{}, len(attrs))
		for _, a := range attrs {
			s.attrs[a] = struct{}{}
		}
	}
}

// Dispatcher fans attribute changes out to matching subscribers.
type Dispatcher struct {
	mu     sync.Mutex
	nextID uint64
	seq    uint64
	subs   map[uint64]*Subscription
	closed bool
}

// New creates an empty Dispatcher.
func New() *Dispatcher {
	return &Dispatcher{subs: make(map[uint64]*Subscription)}
}

// Subscribe registers a subscriber for changes whose entity has prefix as a
// path-style prefix. buffer is the bounded queue size; delivery never blocks
// the producer once the queue is full, applying the configured overflow
// policy instead.
func (d *Dispatcher) Subscribe(prefix string, buffer int, opts ...Option) (*Subscription, error) {
	if buffer <= 0 {
		return nil, ErrInvalidBuffer
	}
	s := &Subscription{
		disp:   d,
		prefix: prefix,
		ch:     make(chan Event, buffer),
		policy: DropOldest,
		tail:   DrainPending,
	}
	for _, opt := range opts {
		opt(s)
	}
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil, ErrClosed
	}
	d.nextID++
	s.id = d.nextID
	d.subs[s.id] = s
	d.mu.Unlock()
	return s, nil
}

// Unsubscribe removes a subscription and closes its channel according to the
// configured tail policy. It is idempotent: unsubscribing an unknown or
// already removed subscription is a harmless no-op.
//
// Unsubscribe never fails a concurrent Publish: fan-out for subscribers that
// are still registered completes under the dispatcher lock, and removal takes
// effect for later publishes only.
func (d *Dispatcher) Unsubscribe(id uint64) {
	d.mu.Lock()
	s, ok := d.subs[id]
	if !ok {
		d.mu.Unlock()
		return
	}
	delete(d.subs, id)
	d.finalize(s)
	d.mu.Unlock()
}

// Publish assigns the next global sequence number to c and delivers the
// resulting event to every matching subscription. Delivery is all-or-nothing
// with respect to dispatcher shutdown: a concurrent Close either happens
// fully before or fully after this call. It never blocks on a slow
// subscriber. After Close it returns ErrClosed.
func (d *Dispatcher) Publish(c Change) (uint64, error) {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return 0, ErrClosed
	}
	d.seq++
	seq := d.seq
	ev := Event{Seq: seq, Change: c}
	for _, s := range d.subs {
		if s.matches(c.Entity, c.Attribute) {
			d.deliver(s, ev)
		}
	}
	d.mu.Unlock()
	return seq, nil
}

// Targets returns the IDs of subscriptions that would receive a change for
// entity/attribute, sorted ascending.
func (d *Dispatcher) Targets(entity, attribute string) []uint64 {
	d.mu.Lock()
	ids := make([]uint64, 0)
	for id, s := range d.subs {
		if s.matches(entity, attribute) {
			ids = append(ids, id)
		}
	}
	d.mu.Unlock()
	sortUint64(ids)
	return ids
}

// Close shuts the dispatcher down. All subscription channels are finalized
// according to their tail policy. Close is idempotent.
//
// A Publish concurrent with Close is serialized against it by the dispatcher
// lock, so it either delivered to every matching subscriber or was rejected
// outright: a fan-out can never be split by shutdown.
func (d *Dispatcher) Close() error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	for id, s := range d.subs {
		delete(d.subs, id)
		d.finalize(s)
	}
	d.mu.Unlock()
	return nil
}

// deliver enqueues ev for s without ever blocking. Called with d.mu held.
func (d *Dispatcher) deliver(s *Subscription, ev Event) {
	if s.closed || s.lagging {
		return
	}
	select {
	case s.ch <- ev:
		return
	default:
	}
	switch s.policy {
	case DropNewest:
		s.recordDrop(ev.Seq)
	case LagDisconnect:
		s.recordDrop(ev.Seq)
		s.lagging = true
		d.finalize(s)
	case DropOldest:
		select {
		case old := <-s.ch:
			// The oldest event is the one actually discarded; account for
			// it by its own sequence so received-vs-dropped sequences
			// partition the published sequence space.
			s.recordDrop(old.Seq)
			select {
			case s.ch <- ev:
			default:
				// Impossible unless a concurrent receiver emptied and the
				// buffer refilled; the buffer is only filled here under
				// d.mu, so fall back to dropping the new event.
				s.recordDrop(ev.Seq)
			}
		default:
			// Buffer became empty concurrently; retry the direct send.
			select {
			case s.ch <- ev:
			default:
				s.recordDrop(ev.Seq)
			}
		}
	}
}

// recordDrop accounts for one discarded event. Called with d.mu held.
func (s *Subscription) recordDrop(seq uint64) {
	atomic.AddUint64(&s.dropped, 1)
	atomic.StoreUint64(&s.lastDrop, seq)
}

// finalize closes the subscription's channel after applying its tail policy.
// Called with d.mu held, which guarantees no sender is mid-send.
func (d *Dispatcher) finalize(s *Subscription) {
	if s.closed {
		return
	}
	if s.tail == DropPending {
		for {
			select {
			case ev := <-s.ch:
				s.recordDrop(ev.Seq)
			default:
				s.closed = true
				close(s.ch)
				return
			}
		}
	}
	s.closed = true
	close(s.ch)
}

func sortUint64(xs []uint64) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j-1] > xs[j]; j-- {
			xs[j-1], xs[j] = xs[j], xs[j-1]
		}
	}
}
