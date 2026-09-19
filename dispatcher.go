package ontology

import (
	"context"
	"sort"
	"sync"
)

// Dispatcher fans property-change events out to matching subscribers.
//
// One mutex serializes publish numbering and every queue mutation, which
// gives three guarantees at once:
//
//   - global sequence numbers are strictly monotonic;
//   - each subscriber sees strictly increasing sequence numbers;
//   - Close cannot interleave with a fan-out, so every Publish is either
//     fully applied to all matching subscribers or rejected wholesale.
//
// Publish never blocks on a consumer: queue overflow is handled entirely
// inside the subscriber by its declared OverflowPolicy.
type Dispatcher struct {
	mu     sync.Mutex
	subs   map[int64]*subscriber
	nextID int64
	seq    int64
	closed bool
}

// New returns an empty, running dispatcher.
func New() *Dispatcher {
	return &Dispatcher{subs: make(map[int64]*subscriber)}
}

// Publish assigns the next global sequence number and fans the event out to
// every matching subscriber. It is non-blocking with respect to slow
// consumers: a full subscriber queue is handled by that subscriber's
// overflow policy and never stalls delivery to anyone else.
//
// After Close it returns ErrClosed and mutates nothing; ctx cancellation
// before dispatch also returns ctx.Err() and assigns no sequence number.
func (d *Dispatcher) Publish(ctx context.Context, entityID, property string, value, oldValue any) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return 0, ErrClosed
	}
	d.seq++
	seq := d.seq
	ev := Event{Seq: seq, EntityID: entityID, Property: property, Value: value, OldValue: oldValue}

	var wake []*subscriber
	for _, s := range d.subs {
		if !s.active || !s.matcher.matches(entityID, property) {
			continue
		}
		if s.offer(ev) {
			wake = append(wake, s)
		}
	}
	d.mu.Unlock()

	// Channel closes happen outside the lock: they cannot block other
	// dispatchers and consumers never need the lock to receive.
	for _, s := range wake {
		s.signal()
	}
	return seq, nil
}

// Targets answers "which subscribers receive this event". Only active,
// registered subscribers are listed. The result is sorted by subscription
// id for stable, deterministic ordering.
func (d *Dispatcher) Targets(entityID, property string) []int64 {
	d.mu.Lock()
	var ids []int64
	for _, s := range d.subs {
		if s.active && s.matcher.matches(entityID, property) {
			ids = append(ids, s.id)
		}
	}
	d.mu.Unlock()
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// Closed reports whether the dispatcher has been closed.
func (d *Dispatcher) Closed() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.closed
}
