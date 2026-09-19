package ontology

import "strings"

// matcher selects messages by entity prefix plus an exact property set.
// An empty property set matches every property of the entity.
type matcher struct {
	prefix     string
	properties map[string]struct{}
}

func newMatcher(prefix string, properties []string) matcher {
	m := matcher{prefix: prefix}
	if len(properties) > 0 {
		m.properties = make(map[string]struct{}, len(properties))
		for _, p := range properties {
			m.properties[p] = struct{}{}
		}
	}
	return m
}

// matches reports whether a message belongs to this subscription. The
// entity check is a strict prefix check (never substring), and the
// property check is exact equality.
func (m matcher) matches(entity, property string) bool {
	if !strings.HasPrefix(entity, m.prefix) {
		return false
	}
	if m.properties == nil {
		return true
	}
	_, ok := m.properties[property]
	return ok
}
