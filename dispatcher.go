package ontology

import (
	"sort"
	"sync"
)

// Dispatcher fans published attribute changes out to matching subscribers.
// Fan-out is serialized by mu: every Publish is applied to all matching
// subscribers atomically with respect to Close and Unsubscribe, so a Close
// can never observe a Publish that reached only part of its recipients.
type Dispatcher struct {
	mu     sync.Mutex
	subs   map[uint64]*registered
	seq    int64
	nextID uint64
	closed bool
}

// NewDispatcher creates an empty dispatcher.
func NewDispatcher() *Dispatcher {
	return &Dispatcher{subs: make(map[uint64]*registered)}
}

// Subscribe registers a subscriber. It returns ErrClosed if the dispatcher
// has been closed and ErrInvalidBuffer for a non-positive buffer.
func (d *Dispatcher) Subscribe(opts SubscribeOptions) (*Subscription, error) {
	if opts.Buffer <= 0 {
		return nil, ErrInvalidBuffer
	}
	box := newMailbox(opts.Buffer, opts.OnOverflow)
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, ErrClosed
	}
	d.nextID++
	sub := &Subscription{id: d.nextID, box: box}
	d.subs[sub.id] = &registered{
		sub:    sub,
		prefix: opts.EntityPrefix,
		attrs:  attrSet(opts.Attributes),
		cancel: opts.OnCancel,
	}
	return sub, nil
}

// Unsubscribe removes a subscriber and applies its CancelPolicy to queued
// messages. It is idempotent: cancelling an unknown, already cancelled, or
// disconnected subscriber returns nil and never panics.
func (d *Dispatcher) Unsubscribe(s *Subscription) error {
	if s == nil {
		return nil
	}
	d.mu.Lock()
	cp := CancelDiscard
	if reg, ok := d.subs[s.id]; ok {
		cp = reg.cancel
		delete(d.subs, s.id)
	}
	d.mu.Unlock()
	s.terminate(cp)
	return nil
}

// Publish stamps the change with a fresh global sequence and fans it out to
// every matching subscriber. Fan-out never blocks: a full subscriber queue is
// handled immediately by that subscriber's own overflow policy. It returns
// ErrClosed once the dispatcher is closed, in which case nothing is delivered.
func (d *Dispatcher) Publish(c Change) (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return 0, ErrClosed
	}
	d.seq++
	seq := d.seq
	delivery := Delivery{Seq: seq, Entity: c.Entity, Attribute: c.Attribute, Value: c.Value}
	for id, reg := range d.subs {
		if !reg.matches(c) {
			continue
		}
		outcome, droppedSeq := reg.sub.box.Put(delivery)
		switch outcome {
		case putDroppedNew:
			reg.sub.recordDrop(droppedSeq)
		case putDisconnect:
			reg.sub.recordDrop(droppedSeq)
			delete(d.subs, id)
			reg.sub.terminate(CancelDrain)
		}
	}
	return seq, nil
}

// Close stops the dispatcher and applies each subscriber's CancelPolicy. It
// is idempotent. Any Publish serialized before Close finishes completely; any
// later Publish fails with ErrClosed and delivers nothing.
func (d *Dispatcher) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	for _, reg := range d.subs {
		reg.sub.terminate(reg.cancel)
	}
	d.subs = make(map[uint64]*registered)
	return nil
}

// SubscribersFor answers, deterministically, which subscriptions would
// receive the given change. The slice is ordered by subscription ID.
func (d *Dispatcher) SubscribersFor(c Change) []MatchInfo {
	d.mu.Lock()
	infos := make([]MatchInfo, 0, len(d.subs))
	for _, reg := range d.subs {
		if !reg.matches(c) {
			continue
		}
		attrs := make([]string, 0, len(reg.attrs))
		for a := range reg.attrs {
			attrs = append(attrs, a)
		}
		sort.Strings(attrs)
		infos = append(infos, MatchInfo{ID: reg.sub.id, EntityPrefix: reg.prefix, Attributes: attrs})
	}
	d.mu.Unlock()
	sort.Slice(infos, func(i, j int) bool { return infos[i].ID < infos[j].ID })
	return infos
}

// nextSeq exposes the current global sequence (last assigned), mainly for
// diagnostics and tests.
func (d *Dispatcher) currentSeq() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.seq
}
