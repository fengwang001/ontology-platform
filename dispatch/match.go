package dispatch

import "strings"

// matchEntity reports whether entity matches the subscription prefix.
// Matching is segment-aware: the entity must equal the prefix or begin with
// prefix followed by '/', so prefix "user" matches "user" and "user/alice"
// but never "superuser" or "userx". An empty prefix matches everything.
func matchEntity(prefix, entity string) bool {
	if prefix == "" {
		return true
	}
	if entity == prefix {
		return true
	}
	return strings.HasPrefix(entity, prefix+"/")
}

// matchAttr reports whether attr is in the subscription's attribute set.
// An empty set matches every attribute; otherwise comparison is exact.
func matchAttr(attrs map[string]struct{}, attr string) bool {
	if len(attrs) == 0 {
		return true
	}
	_, ok := attrs[attr]
	return ok
}

func attrSet(names []string) map[string]struct{} {
	if len(names) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		set[name] = struct{}{}
	}
	return set
}
