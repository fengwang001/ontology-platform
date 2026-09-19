package ontology

import "strings"

// SubscriberInfo is a stable, read-only view of a subscription returned
// by Dispatcher.Match.
type SubscriberInfo struct {
	ID     uint64
	Prefix string
	Attrs  []string // sorted; empty means "matches all attributes"
}

// matches reports whether a change to (entity, attr) should be delivered
// to this subscription. Prefix matching is exact string-prefix matching
// (never substring); attribute matching is exact equality, with an empty
// attribute set matching everything.
func (s *Subscription) matches(entity, attr string) bool {
	if !strings.HasPrefix(entity, s.prefix) {
		return false
	}
	if s.attrs == nil {
		return true
	}
	_, ok := s.attrs[attr]
	return ok
}

// info snapshots the subscription's public match description.
func (s *Subscription) info() SubscriberInfo {
	attrs := make([]string, len(s.attrNames))
	copy(attrs, s.attrNames)
	return SubscriberInfo{ID: s.id, Prefix: s.prefix, Attrs: attrs}
}
