// Package sym holds the binding type and a single lexical scope.
// It depends on no other package in this module.
package sym

import "strconv"

// T is the type a name is bound to.
type T int

const (
	// Int and Bool are the only supported binding types.
	Int T = iota + 1
	Bool
)

// String renders the type as the spellings used by the rules.
func (t T) String() string {
	switch t {
	case Int:
		return "int"
	case Bool:
		return "bool"
	default:
		return "T(" + strconv.Itoa(int(t)) + ")"
	}
}

// Scope is one lexical scope: a name-to-type binding map.
type Scope struct {
	bind map[string]T
}

// New returns an empty scope.
func New() *Scope {
	return &Scope{bind: make(map[string]T)}
}

// Get reports the binding at name. One map probe examines exactly one
// binding (the one stored at the key), never the whole map.
func (s *Scope) Get(name string) (T, bool) {
	t, ok := s.bind[name]
	return t, ok
}

// Put writes the binding at name in this scope only.
func (s *Scope) Put(name string, t T) {
	s.bind[name] = t
}

// Len reports how many bindings live in this scope.
func (s *Scope) Len() int { return len(s.bind) }

// Copy returns a shallow copy of the scope's bindings.
func (s *Scope) Copy() map[string]T {
	m := make(map[string]T, len(s.bind))
	for n, t := range s.bind {
		m[n] = t
	}
	return m
}
