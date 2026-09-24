// Package policy maps roles to their visible column sets.
package policy

import "sort"

// Policy holds, per role, the set of columns that role may see.
// A column not granted is invisible; an unknown role sees nothing.
type Policy struct {
	roles map[string]map[string]bool
}

// New returns an empty policy.
func New() *Policy {
	return &Policy{roles: make(map[string]map[string]bool)}
}

// Grant makes column visible to role, creating the role if needed.
func (p *Policy) Grant(role, column string) {
	cols, ok := p.roles[role]
	if !ok {
		cols = make(map[string]bool)
		p.roles[role] = cols
	}
	cols[column] = true
}

// HasRole reports whether role has any grants at all.
func (p *Policy) HasRole(role string) bool {
	_, ok := p.roles[role]
	return ok
}

// Visible reports whether column is visible to role.
func (p *Policy) Visible(role, column string) bool {
	return p.roles[role][column]
}

// Columns returns the visible columns of role in lexicographic order.
func (p *Policy) Columns(role string) []string {
	set := p.roles[role]
	out := make([]string, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}
