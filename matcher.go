package ontology

// matcher holds a subscription's interest: an entity-ID prefix and a set of
// property names. An empty property set matches every property of matching
// entities.
type matcher struct {
	entityPrefix string
	properties   map[string]struct{}
}

func newMatcher(entityPrefix string, properties []string) matcher {
	m := matcher{entityPrefix: entityPrefix}
	if len(properties) > 0 {
		m.properties = make(map[string]struct{}, len(properties))
		for _, p := range properties {
			m.properties[p] = struct{}{}
		}
	}
	return m
}

// matches reports whether an event addressed to (entityID, property) should
// be delivered to this subscriber.
//
// Entity matching is strict path-style prefix matching, never substring
// matching: an empty prefix matches every entity, and otherwise entityID
// must equal the prefix or continue it at a '/', ':' or '.' boundary. Thus
// prefix "user" matches "user" and "user/42" but never "superuser".
func (m matcher) matches(entityID, property string) bool {
	if !prefixMatch(m.entityPrefix, entityID) {
		return false
	}
	if len(m.properties) == 0 {
		return true
	}
	_, ok := m.properties[property]
	return ok
}

func prefixMatch(prefix, s string) bool {
	if prefix == "" {
		return true
	}
	if len(s) < len(prefix) || s[:len(prefix)] != prefix {
		return false
	}
	if len(s) == len(prefix) {
		return true
	}
	switch s[len(prefix)] {
	case '/', ':', '.':
		return true
	default:
		return false
	}
}
