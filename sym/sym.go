// Package sym defines binding types and the bindings of a single scope.
// It depends on no other package in this module.
package sym

import "sort"

// T is the type a name is bound to.
type T string

// Supported binding types.
const (
	Int  T = "int"
	Bool T = "bool"
)

// Scope holds the name -> type bindings of one lexical scope.
// Reads and writes on a Scope are O(1): names are located via the map,
// never by scanning the bindings.
type Scope struct {
	bind map[string]T
}

// NewScope returns an empty scope.
func NewScope() *Scope {
	return &Scope{bind: make(map[string]T)}
}

// Put writes the name -> t binding into this scope, unconditionally.
// Shadowing/duplicate policy is the caller's responsibility (package scope).
func (s *Scope) Put(name string, t T) {
	s.bind[name] = t
}

// Get reports the binding of name within this scope.
// ok is false when the name is absent; the returned T is the zero value then.
func (s *Scope) Get(name string) (t T, ok bool) {
	t, ok = s.bind[name]
	return t, ok
}

// Len reports the number of bindings in this scope.
func (s *Scope) Len() int {
	return len(s.bind)
}

// Names reports the bound names in this scope, sorted. It lets callers render
// the scope without exposing the underlying map.
func (s *Scope) Names() []string {
	names := make([]string, 0, len(s.bind))
	for n := range s.bind {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
