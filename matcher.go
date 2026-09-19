package ontology

// matchPrefix reports whether entity equals prefix or is a dot-separated
// descendant of it. It is a true prefix match, never a substring match:
// prefix "user" matches "user" and "user.1.name" but not "superuser".
// An empty prefix matches every entity.
func matchPrefix(prefix, entity string) bool {
	if prefix == "" {
		return true
	}
	if len(entity) < len(prefix) || entity[:len(prefix)] != prefix {
		return false
	}
	if len(entity) == len(prefix) {
		return true
	}
	return entity[len(prefix)] == '.'
}

// matchAttribute reports whether attribute is selected by the attribute set.
// An empty set means "all attributes"; otherwise names must be exactly equal.
func matchAttribute(set map[string]struct{}, attribute string) bool {
	if len(set) == 0 {
		return true
	}
	_, ok := set[attribute]
	return ok
}

// registered is the dispatcher's bookkeeping for one subscriber.
type registered struct {
	sub    *Subscription
	prefix string
	attrs  map[string]struct{}
	cancel CancelPolicy
}

func (r *registered) matches(c Change) bool {
	return matchPrefix(r.prefix, c.Entity) && matchAttribute(r.attrs, c.Attribute)
}

// attrSet builds the exact-match lookup set from the option list.
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
