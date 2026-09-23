package view

// Group returns a snapshot of one group. The boolean is false when the group
// does not exist (including a group emptied by deletion) — callers can
// distinguish "absent" from a present group whose aggregates happen to be zero.
func (v *View) Group(name string) (GroupState, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	aggs, ok := v.groups[name]
	if !ok {
		return GroupState{}, false
	}
	st := GroupState{Group: name, Values: map[string]float64{}}
	for _, a := range aggs {
		st.Values[a.Name()] = a.Value()
	}
	for id, val := range v.byGroup[name] {
		st.MemberIDs = append(st.MemberIDs, id)
		_ = val
	}
	return st, true
}

// Groups returns snapshots of every currently existing group key.
func (v *View) Groups() []GroupState {
	v.mu.RLock()
	defer v.mu.RUnlock()
	out := make([]GroupState, 0, len(v.groups))
	for name := range v.groups {
		st := GroupState{Group: name, Values: map[string]float64{}}
		for _, a := range v.groups[name] {
			st.Values[a.Name()] = a.Value()
		}
		for id := range v.byGroup[name] {
			st.MemberIDs = append(st.MemberIDs, id)
		}
		out = append(out, st)
	}
	return out
}

// HasGroup reports whether a group exists.
func (v *View) HasGroup(name string) bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	_, ok := v.groups[name]
	return ok
}

// MemberCount returns the number of records in a group, or 0 if absent.
func (v *View) MemberCount(group string) int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.byGroup[group])
}
