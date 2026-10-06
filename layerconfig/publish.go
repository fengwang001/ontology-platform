package layerconfig

// applyBatch validates and folds one publication into a new immutable state.
// The base state is never mutated: a rejected publication discards all
// intermediate structures. Validation phases run in error-priority order:
// invalid argument, key registration, type/range, batch conflict, lock
// conflict, required missing.
//
// resolve is the read function applied to the candidate for required checks;
// injecting it avoids a package-init cycle.
func applyBatch(base *state, reg *registry, changes []Change,
	resolve func(st *state, target Scope, key string) (Resolved, error)) (*state, error) {
	// Phase A: parameter validity.
	if len(changes) == 0 {
		return nil, errInvalid("publication must contain at least one change")
	}
	layers := make([]Layer, len(changes))
	for i, c := range changes {
		if c.Key == "" {
			return nil, errInvalid("change %d: key must not be empty", i)
		}
		layer, ok := c.Scope.Valid()
		if !ok {
			return nil, errInvalid("change %d key %q: invalid scope env=%q region=%q instance=%q",
				i, c.Key, c.Scope.Env, c.Scope.Region, c.Scope.Instance)
		}
		switch c.Op {
		case OpWrite, OpCancel, OpClear, OpLock, OpUnlock:
		default:
			return nil, errInvalid("change %d key %q: unknown op %d", i, c.Key, c.Op)
		}
		layers[i] = layer
	}

	// Phase C: registration and value type/range.
	schemas := make(map[string]Schema, len(changes))
	for i, c := range changes {
		sc, ok := reg.get(c.Key)
		if !ok {
			return nil, errKey(c.Key)
		}
		schemas[c.Key] = sc
		if c.Op == OpWrite {
			if !valueTypeConsistent(sc, c.Value) {
				return nil, errType("change %d key %q: value type does not match schema", i, c.Key)
			}
			if err := checkValue(sc, c.Value); err != nil {
				return nil, err
			}
		}
	}

	// Phase D: conflicts within this publication. Each (scope,key) may be
	// touched by at most one change, so the publication's effect is
	// unambiguous.
	type target struct {
		scope Scope
		key   string
	}
	seen := make(map[target]int)
	for i, c := range changes {
		t := target{c.Scope, c.Key}
		if first, dup := seen[t]; dup {
			return nil, errBatch("changes %d and %d both target key %q at scope env=%q region=%q instance=%q",
				first, i, c.Key, c.Scope.Env, c.Scope.Region, c.Scope.Instance)
		}
		seen[t] = i
	}

	// Phase E: fold into a private working set.
	w := newWorkingState(base)
	for i, c := range changes {
		sc := schemas[c.Key]
		layer := layers[i]
		fk := scopedKey(c.Scope, c.Key)
		_, hadWrite := w.writes[fk]
		switch c.Op {
		case OpWrite:
			v := normalizeValue(sc, c.Value)
			w.writes[fk] = entry{write: &writeEntry{kind: writeValue, value: v}}
		case OpCancel:
			w.writes[fk] = entry{write: &writeEntry{kind: writeCancel}}
		case OpClear:
			delete(w.writes, fk)
		case OpLock:
			w.locks[fk] = entry{lock: true}
		case OpUnlock:
			delete(w.locks, fk)
		}

		nowWrite := c.Op == OpWrite || c.Op == OpCancel
		_ = hadWrite
		_ = layer
		_ = nowWrite
	}

	// Rebuild the existence index from the surviving writes so an identity
	// whose last write is cleared in this batch disappears immediately.
	w.exists = map[string]entry{}
	for fk := range w.writes {
		sc, layer, ok := parseScopedKey(fk)
		if !ok {
			continue
		}
		for _, ik := range identityKeys(sc, layer) {
			w.exists[ik] = entry{unit: true}
		}
	}

	// Phase F1: an effective lock at a broader scope forbids narrower
	// write/cancel introduced here. The locking layer and broader layers are
	// unconstrained.
	for i, c := range changes {
		if c.Op != OpWrite && c.Op != OpCancel {
			continue
		}
		chain, _ := applicableScopes(c.Scope)
		// Exclude the change's own (last) scope.
		for j := 0; j+1 < len(chain); j++ {
			bs := chain[j]
			if _, ok := w.locks[scopedKey(bs, c.Key)]; ok {
				return nil, errLock("key %q is locked at %s scope env=%q region=%q instance=%q; %s write/cancel rejected",
					c.Key, bs.LayerOf(), bs.Env, bs.Region, bs.Instance, layers[i])
			}
		}
	}

	// Phase F2: a lock must not be introduced while a write survives at a
	// strictly narrower scope in the same chain.
	for i, c := range changes {
		if c.Op != OpLock {
			continue
		}
		layer := layers[i]
		for nk := range w.exists {
			env, region, instance, ok := parseExistenceKey(nk)
			if !ok {
				continue
			}
			ns := Scope{Env: env, Region: region, Instance: instance}
			nlayer, valid := ns.Valid()
			if !valid || !narrowerThan(nlayer, layer) {
				continue
			}
			if !scopePrefixMatch(c.Scope, layer, ns) {
				continue
			}
			if _, present := w.writes[scopedKey(ns, c.Key)]; present {
				return nil, errLock("cannot lock key %q at %s: narrower write at %s scope env=%q region=%q instance=%q must be cleared first",
					c.Key, layer, nlayer, ns.Env, ns.Region, ns.Instance)
			}
		}
	}

	// Phase G: commit, then required-field validation over the candidate.
	candidate := w.commit(base, base.version+1)
	if err := validateRequired(candidate, reg, resolve); err != nil {
		return nil, err
	}
	return candidate, nil
}

// scopePrefixMatch reports whether ns lies in the narrower chain of bs.
func scopePrefixMatch(bs Scope, bl Layer, ns Scope) bool {
	nl, ok := ns.Valid()
	if !ok || !narrowerThan(nl, bl) {
		return false
	}
	if bl >= LayerEnv && ns.Env != bs.Env {
		return false
	}
	if bl >= LayerRegion && ns.Region != bs.Region {
		return false
	}
	return true
}

func parseExistenceKey(k string) (env, region, instance string, ok bool) {
	i1 := indexByte(k, 0)
	if i1 < 0 {
		return "", "", "", false
	}
	rest := k[i1+1:]
	i2 := indexByte(rest, 0)
	if i2 < 0 {
		return "", "", "", false
	}
	return k[:i1], rest[:i2], rest[i2+1:], true
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// validateRequired ensures that, for every environment that exists and every
// region/instance within it, all registered required keys resolve to a value.
// Cost is (existing identities) x (required keys), independent of the total
// key count, layer count and version count.
func validateRequired(st *state, reg *registry,
	resolve func(st *state, target Scope, key string) (Resolved, error)) error {
	required := requiredSchemas(reg)
	if len(required) == 0 {
		return nil
	}
	var idents []identity
	st.exists.forEach(func(k string, _ entry) bool {
		env, region, instance, ok := parseExistenceKey(k)
		if !ok {
			return true
		}
		sc := Scope{Env: env, Region: region, Instance: instance}
		layer, valid := sc.Valid()
		if valid {
			idents = append(idents, identity{sc, layer})
		}
		return true
	})
	// Deterministic order for stable error messages.
	sortIdents(idents)
	for _, id := range idents {
		for _, sc := range required {
			res, err := resolve(st, id.scope, sc.Key)
			if err != nil {
				return err
			}
			if !res.Present {
				return errRequired("required key %q is unset for %s scope env=%q region=%q instance=%q",
					sc.Key, id.layer, id.scope.Env, id.scope.Region, id.scope.Instance)
			}
		}
	}
	return nil
}
