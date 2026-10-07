package derived

import "sort"

// resolveResult is the outcome of reading a (possibly transitive) value
// source at some instance.
type resolveResult struct {
	state EntryState
	keys  []Value
}

// targets returns the ordered distinct targets of typ links leaving id.
func targets(st *state, id, typ string) []string {
	set := st.outAdj[id][typ]
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

func distinctSorted(in []Value) []Value {
	if len(in) <= 1 {
		return in
	}
	sort.Strings(in)
	out := in[:1]
	for _, v := range in[1:] {
		if v != out[len(out)-1] {
			out = append(out, v)
		}
	}
	return out
}

// resolveEffective computes the effective value of decl.SourceProperty at
// instance id, following declaration chains through instance links.
//
// Resolution is a function of the raw link structure and base properties
// only; entries are never read as inputs, so cascading updates cannot depend
// on processing order.
func resolveEffective(st *state, decl *Declaration, id string) resolveResult {
	ts := targets(st, id, decl.LinkType)
	if len(ts) == 0 {
		return resolveResult{state: StateNoLink}
	}
	if decl.RequireUnique && len(ts) > 1 {
		return resolveResult{state: StateNotUnique}
	}
	allKeys := []Value{}
	anyGone := false
	anyNoLink := false
	for _, t := range ts {
		obj := st.objects[t]
		if obj == nil {
			anyGone = true
			continue
		}
		if sub, ok := st.declarations[decl.SourceProperty]; ok {
			r := resolveEffective(st, sub, t)
			switch r.state {
			case StateIndexed:
				allKeys = append(allKeys, r.keys...)
			case StateNoLink:
				anyNoLink = true
			case StateNotUnique:
			case StateNoValue:
			case StateSourceGone:
				anyGone = true
			}
			continue
		}
		if v, ok := obj.Properties[decl.SourceProperty]; ok {
			allKeys = append(allKeys, v)
		} else {
		}
	}
	switch {
	case len(allKeys) > 0:
		return resolveResult{state: StateIndexed, keys: distinctSorted(allKeys)}
	case anyGone:
		return resolveResult{state: StateSourceGone}
	case anyNoLink:
		return resolveResult{state: StateNoLink}
	default:
		return resolveResult{state: StateNoValue}
	}
}

// computeEntry recomputes a single downstream entry for decl from raw state.
func computeEntry(st *state, decl *Declaration, id string) Entry {
	r := resolveEffective(st, decl, id)
	return Entry{Declaration: decl.Name, ObjectID: id, State: r.state, Keys: r.keys}
}

// declarationsForObject returns declarations whose DownstreamType matches id.
func declarationsForObject(st *state, id string) []*Declaration {
	obj := st.objects[id]
	if obj == nil {
		return nil
	}
	var out []*Declaration
	for _, d := range st.declarations {
		if d.DownstreamType == obj.Type {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// AffectedSet maps every downstream instance to the declarations whose
// entries for it must be recomputed.
type AffectedSet map[string]map[string]struct{}

func (a AffectedSet) IDs() []string {
	out := make([]string, 0, len(a))
	for id := range a {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// bfsNode is a reverse value-flow frontier element: property prop changed at
// instance id (base or derived).
type bfsNode struct{ id, prop string }

// propagate computes the exact deduplicated set of derived entries that
// depend on the seeded property changes. For each declaration d reading
// cur.prop, the entry d at every instance linking into cur.id via d.LinkType
// is marked; propagation then continues through that derived property.
func propagate(st *state, seeds []bfsNode, seedsAffected AffectedSet) AffectedSet {
	affected := AffectedSet{}
	for id, set := range seedsAffected {
		affected[id] = map[string]struct{}{}
		for d := range set {
			affected[id][d] = struct{}{}
		}
	}
	visited := map[bfsNode]struct{}{}
	queue := make([]bfsNode, 0, len(seeds))
	for _, s := range seeds {
		if _, ok := visited[s]; !ok {
			visited[s] = struct{}{}
			queue = append(queue, s)
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, d := range st.declarationsBySourceProp(cur.prop) {
			for upstream := range st.inAdj[cur.id][d.LinkType] {
				obj := st.objects[upstream]
				if obj == nil || obj.Type != d.DownstreamType {
					continue
				}
				set := affected[upstream]
				if set == nil {
					set = map[string]struct{}{}
					affected[upstream] = set
				}
				set[d.Name] = struct{}{}
				n := bfsNode{upstream, d.Name}
				if _, ok := visited[n]; !ok {
					visited[n] = struct{}{}
					queue = append(queue, n)
				}
			}
		}
	}
	return affected
}

// affectedByPropertyChange returns the exact deduplicated downstream set for
// a base-property write at sourceID (multi-level chains included).
func affectedByPropertyChange(st *state, sourceID, prop string) AffectedSet {
	return propagate(st, []bfsNode{{sourceID, prop}}, AffectedSet{})
}

// declarationsBySourceProp returns declarations reading prop directly as
// SourceProperty.
func (st *state) declarationsBySourceProp(prop string) []*Declaration {
	var out []*Declaration
	for _, d := range st.declarations {
		if d.SourceProperty == prop {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// createsCycle reports whether adding from-typ->to closes a derivation cycle
// at the instance level, and must be evaluated before the link becomes
// visible.
//
// A resolution state is (id, decl): "derive decl at id". Its transitions go
// to (t, sub) for every t reached via decl.LinkType, where sub is the
// declaration named by decl.SourceProperty; base properties terminate.
// The new edge is dangerous exactly when crossing it enters states from
// which a transition returns to (from, someDecl) whose LinkType is typ,
// i.e. the edge would be traversed again. Pre-existing states are acyclic
// (invariant), so any new cycle must cross the new edge.
func createsCycle(st *state, from, to, typ string) bool {
	step := func(id, linkType string) map[string]struct{} {
		set := map[string]struct{}{}
		for t := range st.outAdj[id][linkType] {
			set[t] = struct{}{}
		}
		if id == from && linkType == typ {
			set[to] = struct{}{}
		}
		return set
	}

	type frame struct{ id, decl string }
	visited := map[frame]struct{}{}
	var dfs func(id, declName string) bool
	dfs = func(id, declName string) bool {
		d := st.declarations[declName]
		if d == nil {
			return false
		}
		for t := range step(id, d.LinkType) {
			sub, chained := st.declarations[d.SourceProperty]
			if !chained {
				continue // base property: chain ends, no cycle
			}
			if t == from && sub.LinkType == typ {
				return true
			}
			f := frame{t, sub.Name}
			if _, seen := visited[f]; seen {
				continue
			}
			visited[f] = struct{}{}
			if dfs(t, sub.Name) {
				return true
			}
		}
		return false
	}

	// States entered immediately after crossing the hypothetical new edge.
	for _, d := range st.declarationsBySourcePropAtLink(typ) {
		if _, chained := st.declarations[d.SourceProperty]; chained {
			start := frame{to, d.SourceProperty}
			visited = map[frame]struct{}{start: {}}
			if dfs(to, d.SourceProperty) {
				return true
			}
		}
	}
	return false
}

// declarationsBySourcePropAtLink returns declarations traversing linkType.
func (st *state) declarationsBySourcePropAtLink(linkType string) []*Declaration {
	var out []*Declaration
	for _, d := range st.declarations {
		if d.LinkType == linkType {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
