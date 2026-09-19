package ontology

import (
	"sort"
	"sync"
)

// Dispatcher fans property changes out to matching subscriptions.
// The zero value is not usable; construct one with New.
//
// Delivery is serialized under a single mutex: every Publish either
// completes its fan-out to all matching subscribers or (after Close)
// has no effect at all. Delivery to any single subscriber never
// blocks, so a slow subscriber cannot stall publishers or peers.
type Dispatcher struct {
	mu     sync.Mutex
	closed bool
	seq    uint64
	nextID uint64
	subs   map[uint64]*Subscription
}

// New returns a ready-to-use dispatcher.
func New() *Dispatcher {
	return &Dispatcher{subs: make(map[uint64]*Subscription)}
}

// Subscribe registers a subscription. It fails with ErrClosed once the
// dispatcher has been closed.
func (d *Dispatcher) Subscribe(opts Options) (*Subscription, error) {
	if opts.Buffer < 1 {
		opts.Buffer = 1
	}
	props := make(map[string]struct{}, len(opts.Properties))
	for _, p := range opts.Properties {
		props[p] = struct{}{}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, ErrClosed
	}
	d.nextID++
	s := &Subscription{
		id:     d.nextID,
		prefix: opts.EntityPrefix,
		props:  props,
		opts:   opts,
		ch:     make(chan Message, opts.Buffer),
		d:      d,
	}
	d.subs[s.id] = s
	return s, nil
}

// Publish assigns the next global sequence number to the change and
// fans it out to every matching subscriber. It never blocks on a slow
// subscriber: a full queue is handled per the subscriber's FullPolicy.
// After Close it returns ErrClosed and delivers nothing.
func (d *Dispatcher) Publish(entity, property string, value any) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return ErrClosed
	}
	d.seq++
	m := Message{Seq: d.seq, Entity: entity, Property: property, Value: value}
	for _, s := range d.sortedLocked() {
		if s.matches(entity, property) {
			s.deliver(m)
		}
	}
	return nil
}

// Match reports which subscriptions would receive a change to
// entity/property, as subscription IDs in stable ascending order.
func (d *Dispatcher) Match(entity, property string) []uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	var ids []uint64
	for _, s := range d.sortedLocked() {
		if s.matches(entity, property) {
			ids = append(ids, s.id)
		}
	}
	return ids
}

// Close shuts the dispatcher down. It is idempotent. Afterwards
// Publish and Subscribe fail with ErrClosed, and every subscription is
// torn down per its DrainOnClose setting. A Publish racing with Close
// either completes fully before Close takes effect or has no effect.
func (d *Dispatcher) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	for _, s := range d.subs {
		s.teardownLocked()
	}
	return nil
}

// sortedLocked returns subscriptions in ascending ID order so that
// fan-out and Match results are deterministic. Caller must hold d.mu.
func (d *Dispatcher) sortedLocked() []*Subscription {
	out := make([]*Subscription, 0, len(d.subs))
	for _, s := range d.subs {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}
