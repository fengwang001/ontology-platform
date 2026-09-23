// Package scope models a single lexical scope: its declarations and
// the link to its parent scope.
package scope

import (
	"errors"

	"ontology/name"
)

// ErrDuplicate rejects a second declaration of the same name in one scope.
var ErrDuplicate = errors.New("scope: duplicate declaration in same scope")

// Scope is one nesting level. Declarations live only in the scope that
// owns them, so leaving a child scope can never mutate its parent.
type Scope struct {
	parent *Scope
	depth  int
	decls  map[string]name.Decl
}

// New creates a scope linked to parent (nil for the outermost scope).
func New(parent *Scope, depth int) *Scope {
	return &Scope{parent: parent, depth: depth, decls: make(map[string]name.Decl)}
}

// Parent returns the enclosing scope, nil at the outermost level.
func (s *Scope) Parent() *Scope { return s.parent }

// Depth returns the nesting depth (0 = outermost).
func (s *Scope) Depth() int { return s.depth }

// Len returns the number of declarations in this scope.
func (s *Scope) Len() int { return len(s.decls) }

// Declare adds d to this scope; duplicate names are rejected.
func (s *Scope) Declare(d name.Decl) error {
	if _, ok := s.decls[d.Name]; ok {
		return ErrDuplicate
	}
	s.decls[d.Name] = d
	return nil
}

// Lookup finds a declaration in this scope only, by hash, without
// scanning the declaration table.
func (s *Scope) Lookup(n string) (name.Decl, bool) {
	d, ok := s.decls[n]
	return d, ok
}
