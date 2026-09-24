package view

import (
	"math"

	"ontology/agg"
	"ontology/change"
)

// Apply validates, journals and maintains the view for one change.
func (v *View) Apply(c change.Change) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.apply(c, true)
}

// apply runs the three phases: Apply (validate + WAL + incremental
// maintenance), Recompute (only for aggregators that need members), and
// Commit (publish version). WAL first: a crash at any point leaves the
// record fully replayable.
func (v *View) apply(c change.Change, wal bool) error {
	if v.hasLast && c.Version == v.last.Version && change.Equal(c, v.last) {
		return nil // duplicate delivery: idempotent skip
	}
	if err := v.validate(c); err != nil {
		return err
	}
	c = c.Normalized()
	if wal && v.journal != nil {
		if err := v.journal.Append(c); err != nil {
			return err
		}
	}
	if v.crashAt == PhaseApply {
		return ErrCrashed
	}
	var old record
	switch c.Op {
	case change.OpInsert:
		v.add(c.ID, c.Group, c.Value)
	case change.OpDelete, change.OpUpdate:
		old = v.records[c.ID]
		v.remove(c.ID, old)
		if c.Op == change.OpUpdate {
			v.add(c.ID, c.Group, c.Value)
		}
	}
	if v.crashAt == PhaseRecompute {
		return ErrCrashed
	}
	if c.Op != change.OpInsert {
		v.recompute(old)
	}
	if v.crashAt == PhasePreCommit {
		return ErrCrashed
	}
	v.last, v.hasLast = c, true
	return nil
}

func (v *View) validate(c change.Change) error {
	var err error
	switch {
	case v.hasLast && c.Version <= v.last.Version:
		err = ErrVersionRegression
	case c.Op != change.OpDelete && !c.HasGroup:
		err = ErrMissingGroup
	case c.Op != change.OpDelete && math.IsNaN(c.Value):
		err = ErrNaN
	case c.Op == change.OpInsert && v.has(c.ID):
		err = ErrDuplicateRecord
	case (c.Op == change.OpDelete || c.Op == change.OpUpdate) && !v.has(c.ID):
		err = ErrUnknownRecord
	case c.Op != change.OpInsert && c.Op != change.OpDelete && c.Op != change.OpUpdate:
		err = ErrBadOp
	}
	if err != nil {
		v.rejected++
	}
	return err
}

func (v *View) has(id uint64) bool {
	_, ok := v.records[id]
	return ok
}

func (v *View) add(id uint64, grp string, val float64) {
	g := v.groups[grp]
	if g == nil {
		g = &group{members: map[uint64]float64{}, valRefs: map[float64]int{}}
		v.groups[grp] = g
	}
	g.members[id] = val
	g.count++
	g.sum += val
	if g.count == 1 || val < g.min {
		g.min = val
	}
	if g.count == 1 || val > g.max {
		g.max = val
	}
	if g.valRefs[val] == 0 {
		g.distinct++
	}
	g.valRefs[val]++
	v.records[id] = record{group: grp, value: val}
}

func (v *View) remove(id uint64, r record) {
	g := v.groups[r.group]
	delete(g.members, id)
	g.count--
	g.sum -= r.value
	g.valRefs[r.value]--
	delete(v.records, id)
	if g.count == 0 {
		delete(v.groups, r.group)
	}
}

// recompute runs the Recompute phase for the group that lost a record:
// only aggregators that declared they need members, and only when the
// concrete deletion leaves the incremental path undecided. The member
// scan never leaves the affected group.
func (v *View) recompute(old record) {
	g := v.groups[old.group]
	if g == nil {
		return
	}
	for _, k := range agg.All() {
		if !k.NeedsMembersOnDelete() {
			continue
		}
		current := map[agg.Kind]float64{agg.Min: g.min, agg.Max: g.max}[k]
		if !k.RecomputeOnDelete(old.value, current) {
			continue
		}
		members := make([]float64, 0, len(g.members))
		for _, val := range g.members {
			members = append(members, val)
		}
		v.recomputes[k]++
		v.memberVisits += int64(len(members))
		switch res := k.Compute(members); k {
		case agg.Min:
			g.min = res
		case agg.Max:
			g.max = res
		case agg.DistinctCount:
			g.distinct = int64(res)
		}
	}
}
