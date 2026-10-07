package consteval

import (
	"sync"
	"sync/atomic"
)

// Registry stores named constants for concurrent registration and
// evaluation. Values are fixed at registration time and never mutated, so
// readers only ever observe immutable snapshots.
//
// Cost model: the store is a hash map from name to final Value. A reference
// resolves with exactly one map lookup — O(1) in the number of registered
// constants and in reference-chain depth, because chains are never walked:
// each registered constant already holds its evaluated value. The
// LookupCount counter makes this externally verifiable.
type Registry struct {
	mu      sync.RWMutex
	consts  map[string]Value
	lookups atomic.Int64
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{consts: make(map[string]Value)}
}

// LookupCount reports how many map lookups have been performed against the
// registry so far. Tests use it to prove that resolving a reference costs
// exactly one lookup regardless of registry size or chain depth.
func (r *Registry) LookupCount() int64 {
	return r.lookups.Load()
}

// lookup performs one map access under the caller's lock.
func (r *Registry) lookup(name string) (Value, bool) {
	r.lookups.Add(1)
	v, ok := r.consts[name]
	return v, ok
}

// Eval evaluates an expression tree against the registry's registered
// constants. A nil registry evaluates trees without references.
//
// Errors surface in the mandated order: structural problems
// (ErrInvalidArgument) first, then unknown names (ErrUnknownName), then
// evaluation errors in left-to-right, children-before-parent order.
func Eval(e *Expr, r *Registry) (Value, error) {
	if e == nil {
		return Value{}, errf(ErrInvalidArgument, "nil expression")
	}
	if err := e.validate(); err != nil {
		return Value{}, err
	}
	if r == nil {
		r = NewRegistry()
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if err := r.checkRefs(e); err != nil {
		return Value{}, err
	}
	return evalExpr(e, r.lookup)
}

// checkRefs reports the first reference to an unregistered name.
func (r *Registry) checkRefs(e *Expr) error {
	var names []string
	e.refNames(&names)
	for _, n := range names {
		if _, ok := r.lookup(n); !ok {
			return errf(ErrUnknownName, "constant %q is not registered", n)
		}
	}
	return nil
}

// Register evaluates expr, fixes the value under name, and makes it
// visible to later expressions. typeName "" declares an untyped constant
// (its kind is preserved); otherwise the result is converted to the named
// type with full representability checks. A rejected registration changes
// no state.
//
// Error priority: ErrInvalidArgument (empty name, unknown type name,
// malformed tree) > ErrDuplicateName > ErrUnknownName > evaluation errors.
func (r *Registry) Register(name, typeName string, expr *Expr) (Value, error) {
	if name == "" {
		return Value{}, errf(ErrInvalidArgument, "constant name must not be empty")
	}
	t, err := ParseType(typeName)
	if err != nil {
		return Value{}, err
	}
	if expr == nil {
		return Value{}, errf(ErrInvalidArgument, "nil expression")
	}
	if err := expr.validate(); err != nil {
		return Value{}, err
	}

	r.mu.RLock()
	if _, dup := r.lookup(name); dup {
		r.mu.RUnlock()
		return Value{}, errf(ErrDuplicateName, "constant %q already registered", name)
	}
	if err := r.checkRefs(expr); err != nil {
		r.mu.RUnlock()
		return Value{}, err
	}
	v, err := evalExpr(expr, r.lookup)
	r.mu.RUnlock()
	if err != nil {
		return Value{}, err
	}
	if t != NoType {
		if v, err = convert(v, t); err != nil {
			return Value{}, err
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.lookup(name); dup {
		return Value{}, errf(ErrDuplicateName, "constant %q already registered", name)
	}
	r.consts[name] = v
	return v, nil
}

// Lookup returns the registered value of name: its stored value, kind and
// (defaulted) type. Cost is one map access, independent of registry size.
func (r *Registry) Lookup(name string) (Value, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lookup(name)
}

// Len reports how many constants are registered.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.consts)
}
