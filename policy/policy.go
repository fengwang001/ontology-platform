// Package policy maps roles to their visible column sets.
package policy

import "sort"

// Policy holds, for each role, the set of columns that role may see.
type Policy struct {
	roles map[string]map[string]struct{}
}

// New returns an empty Policy.
func New() *Policy {
	return &Policy{roles: make(map[string]map[string]struct{})}
}

// Grant makes cols visible to role, replacing any previous grant.
func (p *Policy) Grant(role string, cols ...string) {
	set := make(map[string]struct{}, len(cols))
	for _, c := range cols {
		set[c] = struct{}{}
	}
	p.roles[role] = set
}

// Visible reports whether role is registered and returns its visible set.
// The returned set must not be mutated by the caller.
func (p *Policy) Visible(role string) (map[string]struct{}, bool) {
	set, ok := p.roles[role]
	return set, ok
}

// IsVisible reports whether col is visible to role. An unregistered role
// sees nothing.
func (p *Policy) IsVisible(role, col string) bool {
	set, ok := p.roles[role]
	if !ok {
		return false
	}
	_, ok = set[col]
	return ok
}

// Columns returns the visible columns of role in sorted order. The second
// result reports whether the role is registered.
func (p *Policy) Columns(role string) ([]string, bool) {
	set, ok := p.roles[role]
	if !ok {
		return nil, false
	}
	cols := make([]string, 0, len(set))
	for c := range set {
		cols = append(cols, c)
	}
	sort.Strings(cols)
	return cols, true
}
