// Package policy maps roles to the set of columns they may see.
package policy

import (
	"errors"
	"sort"
)

// ErrUnknownRole is returned when a role has no visibility grant.
var ErrUnknownRole = errors.New("policy: unknown role")

// ErrEmptyPolicy is returned when a policy has no role grants at all.
var ErrEmptyPolicy = errors.New("policy: no role grants")

// Policy is a role -> visible column set map, held in process memory.
// A role with an explicitly empty grant sees no columns (distinct from an
// unknown role, which is an error).
type Policy struct {
	grants map[string]map[string]struct{}
}

// New builds a policy from role -> visible column lists. Duplicate column
// names in a grant are collapsed. A role key with a nil/empty slice is an
// explicit empty visibility grant.
func New(grants map[string][]string) (*Policy, error) {
	if len(grants) == 0 {
		return nil, ErrEmptyPolicy
	}
	p := &Policy{grants: make(map[string]map[string]struct{}, len(grants))}
	for role, cols := range grants {
		set := make(map[string]struct{}, len(cols))
		for _, col := range cols {
			set[col] = struct{}{}
		}
		p.grants[role] = set
	}
	return p, nil
}

// Visible reports whether col is visible to role. An unknown role returns
// ErrUnknownRole.
func (p *Policy) Visible(role, col string) (bool, error) {
	set, ok := p.grants[role]
	if !ok {
		return false, ErrUnknownRole
	}
	_, visible := set[col]
	return visible, nil
}

// VisibleSet returns the visible columns of a role as a sorted slice.
// An unknown role returns ErrUnknownRole.
func (p *Policy) VisibleSet(role string) ([]string, error) {
	set, ok := p.grants[role]
	if !ok {
		return nil, ErrUnknownRole
	}
	cols := make([]string, 0, len(set))
	for col := range set {
		cols = append(cols, col)
	}
	sort.Strings(cols)
	return cols, nil
}

// HasRole reports whether role has a grant (even an empty one).
func (p *Policy) HasRole(role string) bool {
	_, ok := p.grants[role]
	return ok
}
