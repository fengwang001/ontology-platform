// Package policy maps roles to the set of columns they may see.
package policy

import "sort"

// Policy is an immutable role -> visible-column-set mapping.
// The zero value is not usable; build one with New.
type Policy struct {
	visible map[string]map[string]struct{}
}

// New builds a policy. Each role maps to its visible columns;
// an empty slice means the role sees nothing, while a role that
// contains every existing column sees everything.
func New(roleColumns map[string][]string) Policy {
	visible := make(map[string]map[string]struct{}, len(roleColumns))
	for role, cols := range roleColumns {
		set := make(map[string]struct{}, len(cols))
		for _, col := range cols {
			set[col] = struct{}{}
		}
		visible[role] = set
	}
	return Policy{visible: visible}
}

// HasRole reports whether the policy defines the role.
func (p Policy) HasRole(role string) bool {
	_, ok := p.visible[role]
	return ok
}

// Visible reports whether column is visible to role.
// An unknown role sees nothing.
func (p Policy) Visible(role, column string) bool {
	set, ok := p.visible[role]
	if !ok {
		return false
	}
	_, ok = set[column]
	return ok
}

// VisibleSet returns a copy of role's visible columns, sorted.
// It returns an empty (non-nil) slice for an unknown role or a role
// whose visible set is empty.
func (p Policy) VisibleSet(role string) []string {
	set := p.visible[role]
	cols := make([]string, 0, len(set))
	for col := range set {
		cols = append(cols, col)
	}
	sort.Strings(cols)
	return cols
}
