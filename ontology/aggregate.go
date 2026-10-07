package ontology

import (
	"errors"
	"sort"
)

// memberRef identifies one source instance contributing to a view.
type memberRef struct {
	typeName string
	key      string
}

// groupIndex is the per-group derived index: member contributions only.
type groupIndex struct {
	members map[memberRef]float64
}

// viewIndex holds the per-group derived indexes of one aggregate view.
type viewIndex struct {
	spec   ViewSpec
	groups map[string]*groupIndex
}

func newViewIndex(spec ViewSpec) *viewIndex {
	return &viewIndex{spec: spec, groups: make(map[string]*groupIndex)}
}

func (vi *viewIndex) groupForWrite(g string) *groupIndex {
	gi, ok := vi.groups[g]
	if !ok {
		gi = &groupIndex{members: make(map[memberRef]float64)}
		vi.groups[g] = gi
	}
	return gi
}

// contribution computes (group, value) an instance makes to one source of a
// view. ok==false means the instance contributes nothing to this source.
func contribution(spec ViewSpec, src SourceSpec, inst Instance) (g string, val float64, ok bool) {
	gk, present := groupKey(inst.Attrs[src.GroupAttr])
	if !present {
		return "", 0, false
	}
	val = 1
	if spec.Kind == AggSum {
		nv, numeric := numberValue(inst.Attrs[src.ValueAttr])
		if !numeric {
			return "", 0, false
		}
		val = nv
	}
	return gk, val, true
}

// setMember inserts/updates one member contribution within a group.
func (gi *groupIndex) setMember(ref memberRef, val float64) {
	gi.members[ref] = val
}

// removeMember deletes one member; it must already be present in that group.
func (gi *groupIndex) removeMember(ref memberRef) {
	delete(gi.members, ref)
}

// move withdraws a member from old group and inserts into new group. Passing
// oldGroup==newGroup updates in place. Empty old group means it was absent.
func (vi *viewIndex) move(ref memberRef, oldGroup, newGroup string, newVal float64) {
	if oldGroup != "" {
		if gi, ok := vi.groups[oldGroup]; ok {
			gi.removeMember(ref)
			if len(gi.members) == 0 {
				delete(vi.groups, oldGroup)
			}
		}
	}
	if newGroup != "" {
		vi.groupForWrite(newGroup).setMember(ref, newVal)
	}
}

// Maintainer is the incremental aggregate-view maintenance module.
type Maintainer struct {
	views map[string]*viewIndex
}

// NewMaintainer builds a maintainer from view specs.
func NewMaintainer(specs []ViewSpec) *Maintainer {
	m := &Maintainer{views: make(map[string]*viewIndex)}
	for _, s := range specs {
		m.views[s.Name] = newViewIndex(s)
	}
	return m
}

// perSource applies a callback to every (view, source) bound to an instance type.
func (m *Maintainer) perSource(inst Instance, fn func(vi *viewIndex, src SourceSpec)) {
	for _, vi := range m.views {
		for _, src := range vi.spec.Sources {
			if src.Type == inst.TypeName {
				fn(vi, src)
			}
		}
	}
}

// ApplyWrite incrementally moves one member from its old contribution to its new one.
// Migration between groups and multi-view updates are applied here, inside the
// Store's single critical section, so the whole commit is one indivisible
// event relative to every query.
func (m *Maintainer) ApplyWrite(inst Instance, prev *Instance) {
	ref := memberRef{typeName: inst.TypeName, key: inst.Key}
	m.perSource(inst, func(vi *viewIndex, src SourceSpec) {
		oldGroup := ""
		if prev != nil && !prev.Deleted {
			if g, _, ok := contribution(vi.spec, src, *prev); ok {
				oldGroup = g
			}
		}
		newGroup := ""
		var newVal float64
		if !inst.Deleted {
			if g, v, ok := contribution(vi.spec, src, inst); ok {
				newGroup, newVal = g, v
			}
		}
		vi.move(ref, oldGroup, newGroup, newVal)
	})
}

// ApplyDelete removes every contribution of a deleted instance.
func (m *Maintainer) ApplyDelete(inst Instance) {
	tomb := inst
	tomb.Deleted = true
	m.ApplyWrite(tomb, &inst)
}

// Query reads one group's aggregate value in O(member count) time.
// The value is recomputed by summing that group's member map, so cost depends
// only on the group's current membership, never on the total instance count.
func (m *Maintainer) Query(view, group string) (GroupResult, bool) {
	vi, ok := m.views[view]
	if !ok {
		return GroupResult{}, false
	}
	gi, ok := vi.groups[group]
	if !ok {
		return GroupResult{View: view, Group: group, Exists: false}, true
	}
	res := GroupResult{View: view, Group: group, Exists: true}
	for _, v := range gi.members {
		res.Count++
		res.Value += v
	}
	if vi.spec.Kind == AggCount {
		res.Value = float64(res.Count)
	}
	return res, true
}

// memberCount reports the current membership size of one group.
func (m *Maintainer) memberCount(view, group string) int {
	vi, ok := m.views[view]
	if !ok {
		return 0
	}
	if gi, ok := vi.groups[group]; ok {
		return len(gi.members)
	}
	return 0
}

// allGroups recomputes every group of one view from its member maps.
func (m *Maintainer) allGroups(view string) (map[string]GroupResult, error) {
	vi, ok := m.views[view]
	if !ok {
		return nil, errors.New("unknown view " + view)
	}
	out := make(map[string]GroupResult, len(vi.groups))
	for g := range vi.groups {
		r, _ := m.Query(view, g)
		out[g] = r
	}
	return out, nil
}

// groupNames returns sorted group keys (used by diagnostics/benchmarks).
func (m *Maintainer) groupNames(view string) []string {
	vi, ok := m.views[view]
	if !ok {
		return nil
	}
	names := make([]string, 0, len(vi.groups))
	for g := range vi.groups {
		names = append(names, g)
	}
	sort.Strings(names)
	return names
}
