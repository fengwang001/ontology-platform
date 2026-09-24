// Package policy maps roles to their visible column sets and tracks the
// known column universe, so callers can distinguish "column does not
// exist" from "column exists but is invisible to this role".
package policy

// Set is a set of column names.
type Set map[string]bool

// Policy records the column universe and each role's visible columns.
// The zero value is not usable; construct with New.
type Policy struct {
	universe Set
	roles    map[string]Set
}

// New returns an empty policy: no known columns, no grants.
func New() *Policy {
	return &Policy{universe: Set{}, roles: map[string]Set{}}
}

// Declare adds columns to the known universe without granting them to
// any role. A declared but ungranted column is "invisible", not
// "unknown".
func (p *Policy) Declare(cols ...string) {
	for _, c := range cols {
		p.universe[c] = true
	}
}

// Grant makes cols visible to role. Granted columns are also declared.
// Granting the same column twice is a no-op.
func (p *Policy) Grant(role string, cols ...string) {
	s := p.roles[role]
	if s == nil {
		s = Set{}
		p.roles[role] = s
	}
	for _, c := range cols {
		s[c] = true
		p.universe[c] = true
	}
}

// Visible returns a copy of role's visible set. Unknown roles get an
// empty (non-nil) set: nothing is visible.
func (p *Policy) Visible(role string) Set {
	out := Set{}
	for c := range p.roles[role] {
		out[c] = true
	}
	return out
}

// Known reports whether col exists in the declared universe.
func (p *Policy) Known(col string) bool {
	return p.universe[col]
}
