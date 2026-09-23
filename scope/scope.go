// Package scope models one lexical scope with a parent link.
package scope

import (
	"errors"

	"ontology/name"
)

var (
	// ErrDuplicate rejects a repeated name in one scope.
	ErrDuplicate = errors.New("scope: duplicate declaration in scope")
	// ErrLeaveRoot rejects leaving the outermost scope.
	ErrLeaveRoot = errors.New("scope: leave of outermost scope")
)

// Scope is one level of declarations. Shadowing is purely lookup-order
// based: entering a child never mutates the parent's table.
type Scope struct {
	parent *Scope
	depth  int
	decls  map[string]name.Decl
}

// Enter opens a child of parent (nil parent opens the root, depth 0).
func Enter(parent *Scope) *Scope {
	depth := 0
	if parent != nil {
		depth = parent.depth + 1
	}
	return &Scope{parent: parent, depth: depth, decls: make(map[string]name.Decl)}
}

// Leave closes s and returns its parent.
func (s *Scope) Leave() (*Scope, error) {
	if s.parent == nil {
		return nil, ErrLeaveRoot
	}
	return s.parent, nil
}

// Parent returns the enclosing scope, or nil at the root.
func (s *Scope) Parent() *Scope { return s.parent }

// Depth returns the nesting depth (root is 0).
func (s *Scope) Depth() int { return s.depth }

// Len returns the number of declarations in this scope.
func (s *Scope) Len() int { return len(s.decls) }

// Declare adds d; a repeated name is rejected without mutation.
func (s *Scope) Declare(d name.Decl) error {
	key := d.Name().String()
	if _, ok := s.decls[key]; ok {
		return ErrDuplicate
	}
	s.decls[key] = d
	return nil
}

// Lookup finds a declaration in this scope only, by hash.
func (s *Scope) Lookup(n name.Name) (name.Decl, bool) {
	d, ok := s.decls[n.String()]
	return d, ok
}

// Decls returns a snapshot of this scope's declarations.
func (s *Scope) Decls() []name.Decl {
	out := make([]name.Decl, 0, len(s.decls))
	for _, d := range s.decls {
		out = append(out, d)
	}
	return out
}
