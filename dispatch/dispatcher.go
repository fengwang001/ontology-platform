package dispatch

import "sync"

// Dispatcher fans property-change messages out to matching subscriptions.
// The zero value is not usable; create one with New. All methods are safe
// for concurrent use.
//
// Concurrency model: a single mutex serializes sequence-number assignment,
// fan-out, and Close. Per-subscriber queue operations never block, so
// Publish latency is independent of how slow any subscriber drains its
// queue. Close therefore atomically splits history: every Publish either
// completed its full fan-out before Close, or is rejected with ErrClosed
// and delivers nothing.
type Dispatcher struct {
	mu     sync.Mutex
	closed bool
	seq    uint64
	nextID int
	subs   []*Subscription
}

// New returns an open dispatcher with no subscriptions.
func New() *Dispatcher { return &Dispatcher{} }

// Subscribe registers a new subscription. It fails with ErrClosed after
// Close, and with ErrInvalidCapacity when opts.Capacity < 1.
func (d *Dispatcher) Subscribe(opts Options) (*Subscription, error) {
	if opts.Capacity < 1 {
		return nil, ErrInvalidCapacity
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, ErrClosed
	}
	s := newSubscription(d.nextID, opts)
	d.nextID++
	d.subs = append(d.subs, s)
	return s, nil
}

// Publish assigns the next global sequence number to the change and fans it
// out to every matching subscription. It never blocks on a slow subscriber:
// a full queue is handled per that subscription's FullPolicy, independently
// of all other subscriptions. After Close it returns ErrClosed and delivers
// nothing. On success it returns the assigned sequence number.
func (d *Dispatcher) Publish(entity, attr string, value any) (uint64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return 0, ErrClosed
	}
	d.seq++
	m := Message{Seq: d.seq, Entity: entity, Attr: attr, Value: value}
	for _, s := range d.subs {
		if s.matches(entity, attr) {
			s.enqueue(m)
		}
	}
	return m.Seq, nil
}

// Matches returns the active subscriptions that would receive a message for
// (entity, attr), ordered by subscription ID. The result is stable for the
// same set of subscriptions.
func (d *Dispatcher) Matches(entity, attr string) []*Subscription {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []*Subscription
	for _, s := range d.subs {
		if s.active() && s.matches(entity, attr) {
			out = append(out, s)
		}
	}
	return out
}

// Close shuts the dispatcher down. It is idempotent and always returns nil.
// Afterwards Publish and Subscribe fail with ErrClosed. Every subscription
// is finished according to its declared PendingPolicy. A Publish in
// progress when Close is called either completes its fan-out entirely
// before Close returns, or observes the closed state and delivers nothing.
func (d *Dispatcher) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	for _, s := range d.subs {
		s.finish()
	}
	return nil
}
