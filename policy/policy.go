package policy

import "sort"

// Policy maps each role to the columns that role may observe.
type Policy struct {
	visible map[string]map[string]struct{}
}

// New builds a policy. A role with no entries has an empty visible set.
func New(roleColumns map[string][]string) Policy {
	visible := make(map[string]map[string]struct{}, len(roleColumns))
	for role, columns := range roleColumns {
		set := make(map[string]struct{}, len(columns))
		for _, column := range columns {
			set[column] = struct{}{}
		}
		visible[role] = set
	}
	return Policy{visible: visible}
}

// Visible reports whether a column is visible to a role.
func (p Policy) Visible(role, column string) bool {
	_, ok := p.visible[role][column]
	return ok
}

// VisibleColumns returns a sorted copy of a role's visible column set.
func (p Policy) VisibleColumns(role string) []string {
	columns := make([]string, 0, len(p.visible[role]))
	for column := range p.visible[role] {
		columns = append(columns, column)
	}
	sort.Strings(columns)
	return columns
}

// HasRole reports whether the policy contains an explicit role entry.
func (p Policy) HasRole(role string) bool {
	_, ok := p.visible[role]
	return ok
}
