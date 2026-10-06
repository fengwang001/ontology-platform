package layerconfig

// Resolved is the outcome of resolving a key: present, value and scope of the
// effective write.
type Resolved struct {
	Present bool
	Value   Value
}

// Unset is the sentinel returned when no value survives overlay.
var Unset = Resolved{Present: false}

// resolveKey overlays the applicable broad-to-narrow layers for one key.
//
// Semantics:
//   - override (all scalars): the latest surviving value wins;
//   - append (string lists): surviving lists concatenate broad-to-narrow; an
//     element already seen keeps its earliest position and later duplicates
//     are dropped;
//   - an explicit cancellation at a layer voids that layer and every broader
//     layer accumulated so far, for both override and append; narrower writes
//     after it are still applied;
//   - if nothing survives, the key is unset, distinct from "" and [].
//
// It performs exactly one map lookup per applicable layer (<=4), so cost is
// independent of the total number of keys, layers and versions.
func resolveKey(st *state, reg *registry, target Scope, key string) (Resolved, error) {
	chain, err := applicableScopes(target)
	if err != nil {
		return Unset, err
	}
	sc, ok := reg.get(key)
	if !ok {
		return Unset, errKey(key)
	}

	var (
		present bool
		scalar  Value
		list    []string
		seen    map[string]struct{}
	)

	applyList := func(part []string) {
		for _, item := range part {
			if seen == nil {
				seen = map[string]struct{}{}
			}
			if _, dup := seen[item]; dup {
				continue
			}
			seen[item] = struct{}{}
			list = append(list, item)
		}
	}

	for _, scp := range chain {
		e, ok := st.writes.get(scopedKey(scp, key))
		if !ok || e.write == nil {
			continue
		}
		switch e.write.kind {
		case writeCancel:
			// Void this layer and everything broader accumulated so far.
			present = false
			scalar = Value{}
			list = nil
			seen = nil
		case writeValue:
			if sc.Merge == MergeAppend && sc.Type == TypeStringList {
				present = true
				applyList(e.write.value.List)
			} else {
				present = true
				scalar = cloneValue(sc, e.write.value)
				list = nil
				seen = nil
			}
		}
	}

	if !present {
		return Unset, nil
	}
	if sc.Merge == MergeAppend && sc.Type == TypeStringList {
		out := list
		if out == nil {
			// An explicitly written empty list resolves to a non-nil empty
			// list so it stays distinguishable from unset.
			out = []string{}
		}
		return Resolved{Present: true, Value: Value{List: out}}, nil
	}
	return Resolved{Present: true, Value: scalar}, nil
}

func cloneValue(sc Schema, v Value) Value {
	if sc.Type == TypeStringList {
		if v.List == nil {
			return Value{List: []string{}}
		}
		cp := make([]string, len(v.List))
		copy(cp, v.List)
		return Value{List: cp}
	}
	return v
}
