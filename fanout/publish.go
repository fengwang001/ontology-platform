
package fanout

import "sort"

// Publish assigns the next global sequence number to ch and fans it out to
// every currently matching subscription.
//
// The whole fan-out happens under the dispatcher's read lock, while close
// and unsubscribe topology changes take the write lock, so a close can
// never interleave with a publish: the publish is either fully applied to
// every matching subscription or rejected with ErrDispatcherClosed.
// Fan-out enqueues are non-blocking; a slow subscriber can never stall the
// publisher or any other subscriber.
func (d *Dispatcher) Publish(ch Change) (uint64, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.closed {
		return 0, ErrDispatcherClosed
	}

	seq := d.nextSeq + 1
	d.nextSeq = seq
	env := Envelope{Seq: seq, Change: ch}
	for _, sub := range d.subs {
		if sub.matches(ch) {
			sub.deliver(env)
		}
	}
	return seq, nil
}

// matches reports whether the subscription cares about c. Prefix matching
// is exact entity-prefix matching (never substring matching), and attribute
// names must be exactly equal; an empty attribute set matches all attrs.
func (s *Subscription) matches(c Change) bool {
	if !matchesPrefix(s.prefix, c.Entity) {
		return false
	}
	if len(s.attrs) == 0 {
		return true
	}
	_, ok := s.attrs[c.Attr]
	return ok
}

// matchesPrefix reports whether entity is within prefix.
//
// The empty prefix matches every entity. Otherwise entity must equal prefix
// exactly or be a descendant of it, where a descendant boundary is either
// the byte '/' or '.' (the conventional entity hierarchy separators).
// Notably "user" never matches "superuser".
func matchesPrefix(prefix, entity string) bool {
	if prefix == "" {
		return true
	}
	if len(entity) < len(prefix) || entity[:len(prefix)] != prefix {
		return false
	}
	if len(entity) == len(prefix) {
		return true
	}
	b := entity[len(prefix)]
	return b == '/' || b == '.'
}

// SubscribersFor returns the IDs of subscriptions that would receive c, in
// stable ascending subscription-ID order.
func (d *Dispatcher) SubscribersFor(c Change) []uint64 {
	d.mu.RLock()
	ids := make([]uint64, 0, len(d.subs))
	for _, sub := range d.subs {
		if sub.matches(c) {
			ids = append(ids, sub.id)
		}
	}
	d.mu.RUnlock()
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
