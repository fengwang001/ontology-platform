// Package policy maps roles to the set of columns they may see.
package policy

// Policy maps a role name to its set of visible columns.
type Policy map[string]map[string]bool

// New builds a Policy from role -> visible column list.
func New(visible map[string][]string) Policy {
	p := make(Policy, len(visible))
	for role, cols := range visible {
		set := make(map[string]bool, len(cols))
		for _, c := range cols {
			set[c] = true
		}
		p[role] = set
	}
	return p
}

// HasRole reports whether role is known to the policy.
func (p Policy) HasRole(role string) bool {
	_, ok := p[role]
	return ok
}

// Visible reports whether role may see col.
func (p Policy) Visible(role, col string) bool {
	return p[role][col]
}

// Columns returns the visible column set of role (nil for unknown roles).
// The returned set is shared; callers must not mutate it.
func (p Policy) Columns(role string) map[string]bool {
	return p[role]
}
