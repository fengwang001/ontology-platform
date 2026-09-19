package ontology

import (
	"fmt"
	"sort"
	"strconv"
	"sync"
)

// Dispatcher fans property-change messages out to matching subscribers.
// Every subscriber has its own bounded queue, so a slow subscriber never
// blocks Publish or other subscribers.
//
// Concurrency: Publish holds the lock for the whole fan-out, so
// sequence assignment and delivery are atomic with respect to Close
// and to each other. Pushes never block, so this serialization is
// cheap and producers are never stalled by slow subscribers.
type Dispatcher struct {
	mu     sync.RWMutex
	seq    uint64
	subs   map[string]*Subscription
	nextID int
	closed bool
}

// NewDispatcher returns a ready-to-use dispatcher.
func NewDispatcher() *Dispatcher {
	return &Dispatcher{subs: make(map[string]*Subscription)}
}

// Publish assigns the next global sequence number and fans the message
// out to all matching subscribers. It never blocks on a slow subscriber:
// a full queue is handled per that subscriber's FullPolicy. It returns
// ErrClosed after Close.
func (d *Dispatcher) Publish(entity, property string, value any) error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return ErrClosed
	}
	d.seq++
	msg := Message{Seq: d.seq, Entity: entity, Property: property, Value: value}
	sweep := false
	for _, s := range d.subs {
		if !s.isActive() || !s.matcher.matches(entity, property) {
			continue
		}
		if s.queue.push(msg) {
			s.disconnect()
			sweep = true
		}
	}
	d.mu.Unlock()
	if sweep {
		d.sweepInactive()
	}
	return nil
}

// Subscribe registers a new subscription. It fails with ErrClosed after
// Close and with ErrInvalidBuffer for a non-positive BufferSize.
func (d *Dispatcher) Subscribe(opts SubscribeOptions) (*Subscription, error) {
	if opts.BufferSize <= 0 {
		return nil, ErrInvalidBuffer
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, ErrClosed
	}
	d.nextID++
	id := opts.ID
	if id == "" {
		id = "sub-" + strconv.Itoa(d.nextID)
	}
	if _, exists := d.subs[id]; exists {
		return nil, fmt.Errorf("ontology: duplicate subscription id %q", id)
	}
	s := &Subscription{
		id:      id,
		matcher: newMatcher(opts.Prefix, opts.Properties),
		queue:   newQueue(opts.BufferSize, opts.Full, opts.Drain),
		disp:    d,
	}
	s.active.Store(true)
	d.subs[id] = s
	return s, nil
}

// Match returns the IDs of the active subscriptions that would receive
// a message with the given entity and property, in sorted order.
func (d *Dispatcher) Match(entity, property string) []string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	var ids []string
	for _, s := range d.subs {
		if s.isActive() && s.matcher.matches(entity, property) {
			ids = append(ids, s.id)
		}
	}
	sort.Strings(ids)
	return ids
}

// Close shuts the dispatcher down. In-flight Publish calls finish
// completely; later ones fail with ErrClosed. Every subscription's
// queue is finished per its drain policy. Close is idempotent.
func (d *Dispatcher) Close() error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	subs := make([]*Subscription, 0, len(d.subs))
	for _, s := range d.subs {
		subs = append(subs, s)
	}
	d.subs = make(map[string]*Subscription)
	d.mu.Unlock()
	for _, s := range subs {
		s.terminate()
	}
	return nil
}

func (d *Dispatcher) remove(id string) {
	d.mu.Lock()
	delete(d.subs, id)
	d.mu.Unlock()
}

func (d *Dispatcher) sweepInactive() {
	d.mu.Lock()
	for id, s := range d.subs {
		if !s.isActive() {
			delete(d.subs, id)
		}
	}
	d.mu.Unlock()
}
