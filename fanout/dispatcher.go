
package fanout

import "sync"

// Dispatcher assigns global sequence numbers to changes and fans each
// change out to every matching subscription independently.
//
// A single RWMutex serializes topology changes (subscribe/unsubscribe/
// close) against fan-outs: Publish takes a read lock for the whole fan-out,
// so a close can never interleave with a publish and fan-outs run in
// parallel with each other. Per-subscriber queue mechanics are guarded by
// each subscription's own mutex.
type Dispatcher struct {
	mu       sync.RWMutex
	subs     map[uint64]*Subscription
	closed   bool
	nextID   uint64
	nextSeq  uint64
}

// New creates an empty dispatcher.
func New() *Dispatcher {
	return &Dispatcher{subs: make(map[uint64]*Subscription)}
}

// Options configures a new subscription.
type Options struct {
	// Buffer is the bounded queue capacity; it must be positive.
	Buffer int
	// OnFull is the policy applied when the queue is full.
	OnFull DropPolicy
	// OnCancel selects queue treatment at unsubscribe/close time.
	OnCancel CancelPolicy
}

// Subscribe registers a subscriber.
//
// prefix is matched against Change.Entity by exact entity-prefix semantics
// (see matchesPrefix): an empty prefix matches every entity. attrs is the
// set of attribute names of interest; an empty set matches every attribute
// of the matched entity and names must be exactly equal.
func (d *Dispatcher) Subscribe(prefix string, attrs []string, opt Options) (*Subscription, error) {
	if opt.Buffer <= 0 {
		return nil, ErrInvalidArgument
	}
	if !opt.OnFull.valid() || !opt.OnCancel.valid() {
		return nil, ErrInvalidArgument
	}

	set := make(map[string]struct{}, len(attrs))
	for _, a := range attrs {
		set[a] = struct{}{}
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, ErrDispatcherClosed
	}
	d.nextID++
	sub := &Subscription{
		id:       d.nextID,
		prefix:   prefix,
		attrs:    set,
		inCh:     make(chan Envelope, opt.Buffer),
		outCh:    make(chan Envelope),
		drain:    make(chan struct{}),
		purge:    make(chan struct{}),
		drop:     opt.OnFull,
		onCancel: opt.OnCancel,
	}
	d.subs[sub.id] = sub
	go sub.forward()
	return sub, nil
}
