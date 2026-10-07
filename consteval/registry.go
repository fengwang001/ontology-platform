package consteval

import "sync"

// Registry stores named constants. Each constant's value is evaluated
// and fixed at registration time, so referencing a name is a single
// hash-map lookup whose cost does not depend on the number of
// registered constants or on any reference-chain depth.
//
// All methods are safe for concurrent use; their effects are equivalent
// to some serial execution order (a single RWMutex serializes
// registration against evaluation).
type Registry struct {
	mu     sync.RWMutex
	consts map[string]*Const
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{consts: make(map[string]*Const)}
}

// Register evaluates expr and binds the result to name.
//
// If typeName is non-empty, the evaluated value is converted to that
// type (representability enforced) and stored as a typed constant. If
// typeName is empty, the constant is stored untyped, but the
// declaration is a context requiring a concrete type: the value must be
// representable in the default type of its kind (int64 for integers,
// float64 for rationals, bool/string otherwise). A value that is
// already typed (e.g. via explicit conversions in expr) is stored as
// is.
//
// Errors are reported with the priority: invalid argument (empty name,
// malformed tree, unknown type name) > duplicate name > unknown name >
// evaluation errors. A rejected registration changes no state.
func (r *Registry) Register(name string, expr *Node, typeName string) (*Const, error) {
	if name == "" {
		return nil, errf(ErrInvalidArgument, "constant name must not be empty")
	}
	if err := validateStructure(expr); err != nil {
		return nil, err
	}
	var target Type
	if typeName != "" {
		t, ok := ParseType(typeName)
		if !ok {
			return nil, errf(ErrInvalidArgument, "unknown type name %q", typeName)
		}
		target = t
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, dup := r.consts[name]; dup {
		return nil, errf(ErrDuplicateName, "constant %q is already registered", name)
	}
	if err := r.checkNamesKnown(expr); err != nil {
		return nil, err
	}
	v, err := evalNode(expr, r.lookupLocked)
	if err != nil {
		return nil, err
	}
	if typeName != "" {
		cv, cerr := convertTo(v, target)
		if cerr != nil {
			return nil, cerr
		}
		v = cv
	} else if v.Untyped() {
		// Declaration without a type requires representability in the
		// default type, but the constant stays untyped.
		if _, cerr := convertTo(v, DefaultType(v.kind)); cerr != nil {
			return nil, cerr
		}
	}
	r.consts[name] = v
	return v, nil
}

// Eval evaluates expr against the registered constants.
//
// Errors are reported with the priority: invalid argument (malformed
// tree) > unknown name > evaluation errors.
func (r *Registry) Eval(expr *Node) (*Const, error) {
	if err := validateStructure(expr); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if err := r.checkNamesKnown(expr); err != nil {
		return nil, err
	}
	v, err := evalNode(expr, r.lookupLocked)
	if err != nil {
		return nil, err
	}
	return v, nil
}

// Lookup returns the value of a registered constant.
func (r *Registry) Lookup(name string) (*Const, error) {
	if name == "" {
		return nil, errf(ErrInvalidArgument, "constant name must not be empty")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.consts[name]
	if !ok {
		return nil, errf(ErrUnknownName, "unknown constant %q", name)
	}
	return c, nil
}

// Len returns the number of registered constants.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.consts)
}

// lookupLocked resolves a name; the caller must hold the lock.
func (r *Registry) lookupLocked(name string) (*Const, bool) {
	c, ok := r.consts[name]
	return c, ok
}

// checkNamesKnown verifies that every referenced name is registered;
// the caller must hold the lock.
func (r *Registry) checkNamesKnown(expr *Node) *Error {
	names := make(map[string]struct{})
	collectNames(expr, names)
	for n := range names {
		if _, ok := r.consts[n]; !ok {
			return errf(ErrUnknownName, "unknown constant %q", n)
		}
	}
	return nil
}
