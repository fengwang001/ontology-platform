// Package policy maps roles to their visible column sets.
package policy

import (
	"errors"
	"sort"
)

// ErrUnknownColumn is returned when a column is not part of the schema.
var ErrUnknownColumn = errors.New("policy: unknown column")

// Policy binds a schema (the full set of columns) to per-role grants.
type Policy struct {
	columns map[string]struct{}
	roles   map[string]map[string]struct{}
}

// New creates a policy over the given schema columns.
func New(columns ...string) *Policy {
	set := make(map[string]struct{}, len(columns))
	for _, col := range columns {
		set[col] = struct{}{}
	}
	return &Policy{columns: set, roles: map[string]map[string]struct{}{}}
}

// Grant marks the given columns visible to the role.
func (p *Policy) Grant(role string, columns ...string) error {
	set, ok := p.roles[role]
	if !ok {
		set = map[string]struct{}{}
		p.roles[role] = set
	}
	for _, col := range columns {
		if _, ok := p.columns[col]; !ok {
			return ErrUnknownColumn
		}
		set[col] = struct{}{}
	}
	return nil
}

// Visible reports whether the role may see the column. A column outside the
// schema yields ErrUnknownColumn.
func (p *Policy) Visible(role, column string) (bool, error) {
	if _, ok := p.columns[column]; !ok {
		return false, ErrUnknownColumn
	}
	_, ok := p.roles[role][column]
	return ok, nil
}

// VisibleSet returns the sorted visible columns of a role.
func (p *Policy) VisibleSet(role string) []string {
	out := make([]string, 0, len(p.roles[role]))
	for col := range p.roles[role] {
		out = append(out, col)
	}
	sort.Strings(out)
	return out
}

// HiddenSet returns the sorted schema columns the role cannot see.
func (p *Policy) HiddenSet(role string) []string {
	granted := p.roles[role]
	out := make([]string, 0, len(p.columns))
	for col := range p.columns {
		if _, ok := granted[col]; !ok {
			out = append(out, col)
		}
	}
	sort.Strings(out)
	return out
}

// Project keeps only the role-visible entries of row. Invisible keys are
// removed rather than zeroed or nulled.
func (p *Policy) Project(role string, row map[string]string) map[string]string {
	granted := p.roles[role]
	out := make(map[string]string, len(granted))
	for col := range granted {
		if val, ok := row[col]; ok {
			out[col] = val
		}
	}
	return out
}
