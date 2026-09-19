package dispatch

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
)

// Dispatcher fans property-change messages out to matching subscribers.
//
// A single mutex serializes Publish, Subscribe, Cancel and Close. Because
// per-subscriber enqueueing never blocks, holding the lock during fan-out
// cannot stall producers behind slow subscribers, and it gives Close its
// atomicity: an in-flight Publish either completes fully before Close or
// fails with ErrClosed after it.
type Dispatcher struct {
	mu     sync.Mutex
	seq    uint64
	subs   map[string]*Subscriber
	closed bool
	nextID atomic.Uint64
}

// New returns an empty, ready-to-use Dispatcher.
func New() *Dispatcher {
	return &Dispatcher{subs: make(map[string]*Subscriber)}
}

// SubscribeOptions describes a new subscription.
type SubscribeOptions struct {
	// ID uniquely names the subscriber. If empty, one is generated.
	ID string
	// EntityPrefix matches entities with strings.HasPrefix. An empty
	// prefix matches every entity.
	EntityPrefix string
	// Properties restricts matching to these property names (exact
	// equality). Empty means all properties of matching entities.
	Properties []string
	// BufferSize is the bounded queue capacity. Values < 1 become 1.
	BufferSize int
	// Policy selects the full-queue behaviour.
	Policy DropPolicy
	// DrainOnCancel keeps buffered messages receivable after Cancel or
	// Close. When false, buffered messages are discarded and counted as
	// dropped.
	DrainOnCancel bool
}

// Subscribe registers a new subscriber. It fails with ErrClosed once the
// dispatcher is closed, or with an error if the ID is already in use.
func (d *Dispatcher) Subscribe(opts SubscribeOptions) (*Subscriber, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, ErrClosed
	}
	id := opts.ID
	if id == "" {
		id = fmt.Sprintf("sub-%d", d.nextID.Add(1))
	}
	if _, exists := d.subs[id]; exists {
		return nil, fmt.Errorf("dispatch: duplicate subscriber id %q", id)
	}
	size := opts.BufferSize
	if size < 1 {
		size = 1
	}
	props := make(map[string]struct{}, len(opts.Properties))
	for _, p := range opts.Properties {
		props[p] = struct{}{}
	}
	s := &Subscriber{
		id:     id,
		prefix: opts.EntityPrefix,
		props:  props,
		policy: opts.Policy,
		drain:  opts.DrainOnCancel,
		queue:  make(chan Message, size),
		done:   make(chan struct{}),
		d:      d,
	}
	d.subs[id] = s
	return s, nil
}

// Publish assigns a global sequence number to the change and fans it out to
// every matching subscriber without blocking. A full subscriber queue is
// handled per that subscriber's DropPolicy and never affects other
// subscribers. Publish returns ErrClosed after Close.
func (d *Dispatcher) Publish(entity, property string, value any) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return ErrClosed
	}
	d.seq++
	m := Message{Seq: d.seq, Entity: entity, Property: property, Value: value}
	for _, s := range d.subs {
		if s.matches(entity, property) {
			s.enqueue(m)
		}
	}
	return nil
}

// Match returns the sorted IDs of the subscribers that would receive a
// change to property on entity. The result is stable: IDs are sorted.
func (d *Dispatcher) Match(entity, property string) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	var ids []string
	for _, s := range d.subs {
		if s.matches(entity, property) {
			ids = append(ids, s.id)
		}
	}
	sort.Strings(ids)
	return ids
}

// Close shuts the dispatcher down. It is idempotent. After Close, Publish
// and Subscribe fail with ErrClosed. Every subscriber's queue is finished
// according to its DrainOnCancel setting. A Publish racing with Close
// either completes fully or has no effect at all.
func (d *Dispatcher) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	for _, s := range d.subs {
		d.removeLocked(s, !s.drain)
	}
	return nil
}

// removeLocked detaches s and finishes its queue exactly once. When
// discardQueued is true, buffered messages are dropped and counted;
// otherwise they remain receivable after the queue is closed. The caller
// must hold d.mu.
func (d *Dispatcher) removeLocked(s *Subscriber, discardQueued bool) {
	if _, ok := d.subs[s.id]; !ok {
		return
	}
	delete(d.subs, s.id)
	if discardQueued {
		for {
			select {
			case m := <-s.queue:
				s.noteDrop(m.Seq)
			default:
				close(s.queue)
				close(s.done)
				return
			}
		}
	}
	close(s.queue)
	close(s.done)
}
