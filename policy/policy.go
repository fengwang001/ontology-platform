// Package policy maps roles to their visible column sets.
package policy

import "errors"

// ErrUnknownRole is returned when a role has no grant entry.
var ErrUnknownRole = errors.New("policy: unknown role")

// Policy grants each role a set of visible columns. It is not safe for
// concurrent use; build it once and treat it as read-only afterwards.
type Policy struct {
	grants map[string]map[string]bool
}

// New returns an empty Policy.
func New() *Policy {
	return &Policy{grants: map[string]map[string]bool{}}
}

// Grant replaces the visible column set of role with cols.
// Granting zero columns gives the role an explicitly empty visible set,
// which differs from an unknown role.
func (p *Policy) Grant(role string, cols ...string) {
	set := make(map[string]bool, len(cols))
	for _, c := range cols {
		set[c] = true
	}
	p.grants[role] = set
}

// Visible returns a copy of the visible column set of role.
// An empty set means the role may see no column at all.
func (p *Policy) Visible(role string) (map[string]bool, error) {
	set, ok := p.grants[role]
	if !ok {
		return nil, ErrUnknownRole
	}
	out := make(map[string]bool, len(set))
	for c := range set {
		out[c] = true
	}
	return out, nil
}
