package ontology

// Options configures a subscription. Zero values are valid except Capacity,
// which defaults to 16 when not positive.
type Options struct {
	// Capacity is the subscriber's bounded queue size.
	Capacity int
	// Policy decides full-queue behavior; zero value is DropOldest.
	Policy OverflowPolicy
	// DrainOnUnsubscribe controls unsubscribe (and dispatcher Close)
	// semantics: true lets the consumer finish buffered events (Next then
	// returns ErrDrained); false discards the buffer immediately (Next then
	// returns ErrSubscriptionGone).
	DrainOnUnsubscribe bool
}

const defaultCapacity = 16

// Subscribe registers a subscriber interested in the given entity-ID prefix
// and property names. An empty property list matches all properties of
// matching entities. It fails with ErrClosed after the dispatcher is closed.
func (d *Dispatcher) Subscribe(entityPrefix string, properties []string, opts Options) (*Subscription, error) {
	capacity := opts.Capacity
	if capacity <= 0 {
		capacity = defaultCapacity
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, ErrClosed
	}
	d.nextID++
	s := &subscriber{
		id:             d.nextID,
		matcher:        newMatcher(entityPrefix, properties),
		queue:          newRing(capacity),
		policy:         opts.Policy,
		drainRemaining: opts.DrainOnUnsubscribe,
		active:         true,
		notify:         make(chan struct{}),
	}
	d.subs[s.id] = s
	return &Subscription{d: d, s: s}, nil
}

// unsubscribeID removes a subscriber from fan-out. It is the single
// implementation behind Subscription.Unsubscribe and Dispatcher.Unsubscribe,
// and it is idempotent: unknown or already-terminal ids are a no-op.
func (d *Dispatcher) unsubscribeID(id int64) {
	d.mu.Lock()
	s, ok := d.subs[id]
	if !ok || !s.active {
		d.mu.Unlock()
		return
	}
	s.active = false
	s.removed = true
	if !s.drainRemaining {
		s.queue.clear()
	}
	delete(d.subs, id)
	notify := s.notify
	d.mu.Unlock()

	// Wake a blocked Next exactly once so it observes the terminal state.
	close(notify)
}

// Unsubscribe detaches a subscription by id; a no-op when unknown. Prefer
// Subscription.Unsubscribe on an existing handle.
func (d *Dispatcher) Unsubscribe(id int64) { d.unsubscribeID(id) }

// Close shuts the dispatcher down deterministically:
//
//   - subsequent Publish calls return ErrClosed and deliver nothing;
//   - subsequent Subscribe calls return ErrClosed;
//   - each subscriber's queue is finalized per its DrainOnUnsubscribe
//     declaration (drained to completion or discarded);
//   - a Publish already in progress completes as one atomic unit because
//     fan-out and the closed transition share the same lock;
//   - closing more than once is a safe no-op returning nil.
func (d *Dispatcher) Close() error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	pending := d.subs
	d.subs = make(map[int64]*subscriber)
	for _, s := range pending {
		s.active = false
		s.removed = true
		if !s.drainRemaining {
			s.queue.clear()
		}
	}
	d.mu.Unlock()

	for _, s := range pending {
		close(s.notify)
	}
	return nil
}
