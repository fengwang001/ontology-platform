// Package policy maps roles to their visible column sets.
package policy

import "sort"

// Policy holds, in process memory, the set of visible columns per role.
type Policy struct {
	roles map[string]map[string]bool
}

// New returns an empty policy.
func New() *Policy {
	return &Policy{roles: map[string]map[string]bool{}}
}

// Grant makes cols visible to role. Granting no columns registers the role
// with an empty visible set.
func (p *Policy) Grant(role string, cols ...string) {
	set, ok := p.roles[role]
	if !ok {
		set = map[string]bool{}
		p.roles[role] = set
	}
	for _, c := range cols {
		set[c] = true
	}
}

// Known reports whether role has any grant entry (possibly empty).
func (p *Policy) Known(role string) bool {
	_, ok := p.roles[role]
	return ok
}

// Visible reports whether col is visible to role.
func (p *Policy) Visible(role, col string) bool {
	return p.roles[role][col]
}

// VisibleSet returns the visible columns of role in lexicographic order.
func (p *Policy) VisibleSet(role string) []string {
	set := p.roles[role]
	out := make([]string, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}
