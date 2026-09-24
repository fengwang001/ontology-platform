package policy

import "sort"

type Policy struct {
	roles map[string]map[string]struct{}
}

func New(visible map[string][]string) *Policy {
	roles := make(map[string]map[string]struct{}, len(visible))
	for role, columns := range visible {
		set := make(map[string]struct{}, len(columns))
		for _, column := range columns {
			set[column] = struct{}{}
		}
		roles[role] = set
	}
	return &Policy{roles: roles}
}

func (p *Policy) HasRole(role string) bool {
	_, ok := p.roles[role]
	return ok
}

func (p *Policy) IsVisible(role, column string) bool {
	set, ok := p.roles[role]
	if !ok {
		return false
	}
	_, ok = set[column]
	return ok
}

func (p *Policy) VisibleColumns(role string) []string {
	set := p.roles[role]
	columns := make([]string, 0, len(set))
	for column := range set {
		columns = append(columns, column)
	}
	sort.Strings(columns)
	return columns
}
