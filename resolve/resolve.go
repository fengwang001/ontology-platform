// Package resolve walks the scope chain from a reference point outward
// and returns the unique matching declaration plus its scope depth.
package resolve

import (
	"errors"
	"sync/atomic"

	"ontology/name"
	"ontology/scope"
)

var (
	// ErrUndefined: no scope on the chain declares the name.
	ErrUndefined = errors.New("resolve: undefined name")
	// ErrUseBeforeDecl: name found, but the reference position is before
	// the declaration position and the kind forbids forward references.
	ErrUseBeforeDecl = errors.New("resolve: use before declaration")
)

// Hit is the outcome of a successful resolution: the declaration and the
// depth of the scope that owns it.
type Hit struct {
	Decl  name.Decl
	Depth int
}

// Resolver resolves names. scanned counts how many declaration entries
// the most recent Resolve inspected (one hash lookup per scope level).
type Resolver struct {
	scanned atomic.Int64
}

// Resolve finds the innermost scope declaring n. The first scope on the
// chain that contains n wins (shadowing); the position only decides
// legality: pos before the declaration is allowed for name.Forward and
// is ErrUseBeforeDecl otherwise. The error never falls through to outer
// scopes once a scope declares n.
func (r *Resolver) Resolve(s *scope.Scope, n string, pos int) (Hit, error) {
	r.scanned.Store(0)
	for cur := s; cur != nil; cur = cur.Parent() {
		r.scanned.Add(1)
		d, ok := cur.Lookup(n)
		if !ok {
			continue
		}
		if pos >= d.Pos || d.Kind == name.Forward {
			return Hit{Decl: d, Depth: cur.Depth()}, nil
		}
		return Hit{}, ErrUseBeforeDecl
	}
	return Hit{}, ErrUndefined
}
