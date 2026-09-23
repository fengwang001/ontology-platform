// Package resolve binds a name reference by walking the scope chain.
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
	// ErrUseBeforeDeclare: innermost declaration is NoForward and the
	// reference position precedes the declaration position.
	ErrUseBeforeDeclare = errors.New("resolve: use before declaration")
)

// Result is a unique, attributable binding: the declaration plus the
// depth of the scope it was found in.
type Result struct {
	Decl  name.Decl
	Depth int
}

// Record is a logged reference for later capture analysis and SelfCheck.
type Record struct {
	Scope  *scope.Scope
	Name   name.Name
	Pos    int
	Result Result
}

// Resolver resolves references. The looked field counts how many
// declaration entries the most recent Resolve examined (one per
// hash lookup, i.e. one per scope visited); it is intentionally
// unexported and not part of any public result.
type Resolver struct {
	looked int64
}

// New returns a ready-to-use Resolver.
func New() *Resolver { return &Resolver{} }

// Resolve walks outward from s. The first scope declaring the name wins
// for the whole scope: Forward declarations bind at any position,
// NoForward declarations only at positions at or after their own.
func (r *Resolver) Resolve(s *scope.Scope, n name.Name, refPos int) (Result, error) {
	var looked int64
	for cur := s; cur != nil; cur = cur.Parent() {
		looked++
		d, ok := cur.Lookup(n)
		if !ok {
			continue
		}
		atomic.StoreInt64(&r.looked, looked)
		if d.Kind().AllowsForward() || d.Pos() <= refPos {
			return Result{Decl: d, Depth: cur.Depth()}, nil
		}
		return Result{}, ErrUseBeforeDeclare
	}
	atomic.StoreInt64(&r.looked, looked)
	return Result{}, ErrUndefined
}
