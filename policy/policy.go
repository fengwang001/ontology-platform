// Package policy maps roles to the set of columns they may see.
package policy

import "sort"

// Policy is a column universe plus per-role visible column sets.
//
// The zero value is not usable; create one with New.
type Policy struct {
	columns map[string]struct{}
	roles   map[string]map[string]struct{}
}

// New creates a policy whose universe is exactly the given columns.
func New(columns []string) *Policy {
	p := &Policy{
		columns: make(map[string]struct{}, len(columns)),
		roles:   make(map[string]map[string]struct{}),
	}
	for _, column := range columns {
		p.columns[column] = struct{}{}
	}
	return p
}

// Grant marks the given columns visible to the role.
// Columns outside the universe and duplicates are ignored.
func (p *Policy) Grant(role string, columns ...string) {
	visible, ok := p.roles[role]
	if !ok {
		visible = make(map[string]struct{})
		p.roles[role] = visible
	}
	for _, column := range columns {
		if _, known := p.columns[column]; known {
			visible[column] = struct{}{}
		}
	}
}

// HasColumn reports whether column belongs to the universe.
func (p *Policy) HasColumn(column string) bool {
	_, ok := p.columns[column]
	return ok
}

// KnownRole reports whether the role was ever granted to (even nothing).
func (p *Policy) KnownRole(role string) bool {
	_, ok := p.roles[role]
	return ok
}

// Visible reports whether role may see column.
// Unknown columns and unknown roles are never visible.
func (p *Policy) Visible(role, column string) bool {
	visible, ok := p.roles[role]
	if !ok {
		return false
	}
	_, ok = visible[column]
	return ok
}

// VisibleSet returns a snapshot of the role's visible columns as a set.
// An unknown role yields the empty set.
func (p *Policy) VisibleSet(role string) map[string]struct{} {
	out := make(map[string]struct{})
	for column := range p.roles[role] {
		out[column] = struct{}{}
	}
	return out
}

// VisibleList returns the role's visible columns sorted lexicographically.
func (p *Policy) VisibleList(role string) []string {
	out := make([]string, 0, len(p.roles[role]))
	for column := range p.roles[role] {
		out = append(out, column)
	}
	sort.Strings(out)
	return out
}

// Universe returns all universe columns sorted lexicographically.
func (p *Policy) Universe() []string {
	out := make([]string, 0, len(p.columns))
	for column := range p.columns {
		out = append(out, column)
	}
	sort.Strings(out)
	return out
}

// HiddenList returns the universe columns not visible to role, sorted.
func (p *Policy) HiddenList(role string) []string {
	visible := p.roles[role]
	out := make([]string, 0)
	for column := range p.columns {
		if _, seen := visible[column]; !seen {
			out = append(out, column)
		}
	}
	sort.Strings(out)
	return out
}
