package ontology

import (
	"sort"
	"sync/atomic"
)

// Subscription is one subscriber's registration.
type Subscription struct {
	disp     *Dispatcher
	id       uint64
	prefix   string
	attrs    map[string]struct{}
	ch       chan Event
	policy   OverflowPolicy
	tail     TailPolicy
	dropped  uint64
	lastDrop uint64
	lagging  bool
	closed   bool
}

// C returns the channel from which the subscriber receives events. The
// channel is closed exactly once, on unsubscribe (or dispatcher close).
func (s *Subscription) C() <-chan Event { return s.ch }

// Stats returns a point-in-time snapshot of this subscription's counters.
//
// Stats is safe to call concurrently with Publish; it is the only accessor
// that may be used after the subscription has been removed.
func (s *Subscription) Stats() Stats {
	dropped := atomic.LoadUint64(&s.dropped)
	lastDrop := atomic.LoadUint64(&s.lastDrop)
	s.disp.mu.Lock()
	lagging, closed := s.lagging, s.closed
	s.disp.mu.Unlock()
	return Stats{Dropped: dropped, LastDropSeq: lastDrop, Lagging: lagging, Closed: closed}
}

// ID returns the subscription's stable identifier.
func (s *Subscription) ID() uint64 { return s.id }

// matches reports whether the change should be delivered to this subscriber.
// Matching requires the entity to have prefix as a path-style prefix (no
// substring match) and the attribute to be a set member (empty set = all).
func (s *Subscription) matches(entity, attribute string) bool {
	if !prefixMatch(s.prefix, entity) {
		return false
	}
	if len(s.attrs) == 0 {
		return true
	}
	_, ok := s.attrs[attribute]
	return ok
}

// prefixMatch reports whether s is equal to path or contains it as a
// path-style prefix: s == "user" matches "user" and "user/42" but never
// "superuser".
func prefixMatch(s, path string) bool {
	if s == "" {
		return true
	}
	if len(path) < len(s) {
		return false
	}
	if path[:len(s)] != s {
		return false
	}
	if len(path) == len(s) {
		return true
	}
	return path[len(s)] == '/'
}

// attributeNames returns the sorted attribute set (empty means "all").
func (s *Subscription) attributeNames() []string {
	names := make([]string, 0, len(s.attrs))
	for name := range s.attrs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
