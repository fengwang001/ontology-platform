package view

import "ontology/agg"

// insert adds a member and feeds every aggregator of the group.
func (v *View) insert(id, group string, value float64) {
	aggs, ok := v.groups[group]
	if !ok {
		aggs = v.factory()
		v.groups[group] = aggs
		v.byGroup[group] = map[string]float64{}
	}
	for _, a := range aggs {
		a.Insert(value)
	}
	v.members[id] = member{group: group, value: value}
	v.byGroup[group][id] = value
}

// remove deletes a member. Count/Sum withdraw in place. Aggregators that need
// members and report a hit (e.g. deleting the current Min) are marked dirty;
// the affected group is dropped entirely when its last member is removed.
func (v *View) remove(id, group string, value float64, dirty map[string]map[int]bool) {
	aggs := v.groups[group]
	for i, a := range aggs {
		if !a.NeedsMembersOnDelete() {
			a.Delete(value)
			continue
		}
		if a.Delete(value) {
			if dirty[group] == nil {
				dirty[group] = map[int]bool{}
			}
			dirty[group][i] = true
		}
	}
	delete(v.members, id)
	delete(v.byGroup[group], id)
	if len(v.byGroup[group]) == 0 {
		delete(v.groups, group)
		delete(v.byGroup, group)
		delete(dirty, group)
	}
}

// recomputeDirty rebuilds only the flagged aggregators of each flagged group,
// scanning only that group's surviving members.
func (v *View) recomputeDirty(dirty map[string]map[int]bool) {
	for group, idxs := range dirty {
		aggs, ok := v.groups[group]
		if !ok {
			continue
		}
		mems := v.byGroup[group]
		for i := range idxs {
			a := aggs[i]
			a.Reset()
			v.memberVisits[a.Name()] += len(mems)
			v.recompCount[a.Name()]++
		}
		j := 0
		for _, val := range mems {
			for i := range idxs {
				aggs[i].Insert(val)
			}
			if j == 0 {
				v.crash(PhaseRecompute)
			}
			j++
		}
	}
}

var _ = agg.NewCount
