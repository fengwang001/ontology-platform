package resolver

// constVarBase offsets variables that are treated as distinct,
// non-substitutable constants during the more-specialized check.
const constVarBase = 1000

// match reports whether pattern matches target, i.e. whether there is a
// substitution of the pattern's variables such that pattern becomes
// structurally equal to target. Every occurrence of a variable must map to
// the same type (non-linear patterns are supported). Variables occurring
// in the target are treated as constants. On success the substitution is
// returned.
func match(pattern, target *Type) (map[int]*Type, bool) {
	subst := map[int]*Type{}
	if !matchInto(pattern, target, subst) {
		return nil, false
	}
	return subst, true
}

func matchInto(pattern, target *Type, subst map[int]*Type) bool {
	if pattern.IsVar {
		if bound, ok := subst[pattern.Var]; ok {
			return typeEqual(bound, target)
		}
		subst[pattern.Var] = target
		return true
	}
	if target.IsVar {
		return false
	}
	if pattern.Name != target.Name || len(pattern.Args) != len(target.Args) {
		return false
	}
	for i := range pattern.Args {
		if !matchInto(pattern.Args[i], target.Args[i], subst) {
			return false
		}
	}
	return true
}

// constify replaces every variable of t with a distinct constant marker.
func constify(t *Type) *Type {
	if t.IsVar {
		return &Type{IsVar: true, Var: t.Var + constVarBase}
	}
	args := make([]*Type, len(t.Args))
	for i, a := range t.Args {
		args[i] = constify(a)
	}
	return &Type{Name: t.Name, Args: args}
}

// moreSpecialized reports whether an instance with head a is more
// specialized than an instance with head b: treating a's variables as
// pairwise distinct non-substitutable constants, b's head must match it.
func moreSpecialized(a, b *Type) bool {
	_, ok := match(b, constify(a))
	return ok
}

// applySubst substitutes the variables of t according to subst. Variables
// of a well-formed instance context always occur in the head and are
// therefore always bound after a successful head match; an unbound
// variable is kept as-is defensively.
func applySubst(t *Type, subst map[int]*Type) *Type {
	if t.IsVar {
		if v, ok := subst[t.Var]; ok {
			return v
		}
		return t
	}
	args := make([]*Type, len(t.Args))
	for i, a := range t.Args {
		args[i] = applySubst(a, subst)
	}
	return &Type{Name: t.Name, Args: args}
}
