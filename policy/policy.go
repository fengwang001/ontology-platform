// Package policy maps a role to the set of columns it may see.
package policy

import "sort"

// Policy is the visibility grant for one role.
type Policy struct {
	role    string
	visible map[string]struct{}
}

// New builds a Policy granting the role visibility of exactly cols.
func New(role string, cols []string) Policy {
	visible := make(map[string]struct{}, len(cols))
	for _, c := range cols {
		visible[c] = struct{}{}
	}
	return Policy{role: role, visible: visible}
}

// Role returns the role this policy belongs to.
func (p Policy) Role() string { return p.role }

// Visible reports whether the role may see column col.
func (p Policy) Visible(col string) bool {
	_, ok := p.visible[col]
	return ok
}

// Columns returns the visible column names in sorted order.
func (p Policy) Columns() []string {
	cols := make([]string, 0, len(p.visible))
	for c := range p.visible {
		cols = append(cols, c)
	}
	sort.Strings(cols)
	return cols
}
