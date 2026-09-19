package ontology

import (
	"sort"
	"sync"
	"sync/atomic"
)

// Dispatcher fans attribute-change messages out to matching subscribers.
//
// Concurrency model: pubMu serializes Publish against Close, so a Publish
// in flight when Close is called always completes fully (or, if it starts
// after Close, has no effect at all). subsMu guards the registry, and each
// Subscription has its own mutex for queue operations. Lock order is
// pubMu -> subsMu -> subscription mutex. The zero value is not usable;
// call New.
type Dispatcher struct {
	pubMu  sync.Mutex
	subsMu sync.RWMutex
	subs   map[uint64]*Subscription
	seq    uint64
	nextID uint64
	closed atomic.Bool
}

// New returns an empty, ready-to-use Dispatcher.
func New() *Dispatcher {
	return &Dispatcher{subs: make(map[uint64]*Subscription)}
}

// Subscribe registers a new subscription. It fails with ErrInvalidOptions
// for bad options and with ErrClosed after Close.
func (d *Dispatcher) Subscribe(opts SubscribeOptions) (*Subscription, error) {
	if opts.Capacity < 1 || !opts.Policy.valid() {
		return nil, ErrInvalidOptions
	}
	d.subsMu.Lock()
	defer d.subsMu.Unlock()
	if d.closed.Load() {
		return nil, ErrClosed
	}
	d.nextID++
	s := &Subscription{
		d:             d,
		id:            d.nextID,
		prefix:        opts.Prefix,
		attrs:         attrSet(opts.Attrs),
		attrNames:     sortedAttrs(opts.Attrs),
		policy:        opts.Policy,
		drainOnCancel: opts.DrainOnCancel,
		ch:            make(chan Message, opts.Capacity),
	}
	d.subs[s.id] = s
	return s, nil
}

// Publish assigns the next global sequence number to the change and fans
// it out to every matching subscriber. It never blocks on a slow
// subscriber: each subscriber's overflow Policy is applied independently.
// It returns the assigned sequence number, or ErrClosed after Close.
func (d *Dispatcher) Publish(entity, attr string, value any) (uint64, error) {
	d.pubMu.Lock()
	defer d.pubMu.Unlock()
	if d.closed.Load() {
		return 0, ErrClosed
	}
	d.seq++
	m := Message{Seq: d.seq, Entity: entity, Attr: attr, Value: value}

	d.subsMu.RLock()
	targets := make([]*Subscription, 0, len(d.subs))
	for _, s := range d.subs {
		if s.matches(entity, attr) {
			targets = append(targets, s)
		}
	}
	d.subsMu.RUnlock()

	var disconnected []uint64
	for _, s := range targets {
		if s.deliver(m) {
			disconnected = append(disconnected, s.id)
		}
	}
	if len(disconnected) > 0 {
		d.subsMu.Lock()
		for _, id := range disconnected {
			delete(d.subs, id)
		}
		d.subsMu.Unlock()
	}
	return m.Seq, nil
}

// Match reports which subscriptions would receive a change to
// (entity, attr), ordered by subscription ID for a stable result.
func (d *Dispatcher) Match(entity, attr string) []SubscriberInfo {
	d.subsMu.RLock()
	defer d.subsMu.RUnlock()
	out := make([]SubscriberInfo, 0, len(d.subs))
	for _, s := range d.subs {
		if s.matches(entity, attr) {
			out = append(out, s.info())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Close shuts the dispatcher down. It is idempotent. A Publish in flight
// either completes fully before Close returns or has no effect. After
// Close, Publish and Subscribe fail with ErrClosed, and every
// subscription's queue is finished per its DrainOnCancel option.
func (d *Dispatcher) Close() error {
	d.pubMu.Lock()
	defer d.pubMu.Unlock()
	if !d.closed.CompareAndSwap(false, true) {
		return nil
	}
	d.subsMu.Lock()
	subs := make([]*Subscription, 0, len(d.subs))
	for _, s := range d.subs {
		subs = append(subs, s)
	}
	d.subs = make(map[uint64]*Subscription)
	d.subsMu.Unlock()

	for _, s := range subs {
		s.mu.Lock()
		s.closeLocked()
		s.mu.Unlock()
	}
	return nil
}

// removeSub detaches s from the registry and terminates it. It is
// idempotent: only the first call for a subscription takes effect.
func (d *Dispatcher) removeSub(s *Subscription) {
	d.subsMu.Lock()
	if _, ok := d.subs[s.id]; !ok {
		d.subsMu.Unlock()
		return
	}
	delete(d.subs, s.id)
	d.subsMu.Unlock()

	s.mu.Lock()
	s.closeLocked()
	s.mu.Unlock()
}

// attrSet builds the exact-match attribute set; nil means "match all".
func attrSet(attrs []string) map[string]struct{} {
	if len(attrs) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(attrs))
	for _, a := range attrs {
		set[a] = struct{}{}
	}
	return set
}

// sortedAttrs returns a deduplicated, sorted copy of attrs.
func sortedAttrs(attrs []string) []string {
	if len(attrs) == 0 {
		return nil
	}
	set := attrSet(attrs)
	out := make([]string, 0, len(set))
	for a := range set {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}
