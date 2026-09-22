package view

import (
	"hash/fnv"

	"ontology/agg"
	"ontology/change"
)

// mutation is one per-group membership delta belonging to a change.
type mutation struct {
	group   string
	key     string
	removed bool // true = delete member, false = add member
	value   float64
}

// plan holds everything one Submit will touch, computed before mutation.
type plan struct {
	muts    []mutation
	touched map[string]*groupState
	// keyTransition, when keyChanged is true, replaces the members-table
	// entry for key with entry; for inserts keyChanged with absent old
	// entry, for deletes entryPresent stays false.
	key          string
	entry        memberEntry
	entryPresent bool
}

func (v *View) planChange(c change.Change) *plan {
	p := &plan{touched: map[string]*groupState{}}
	switch c.Op {
	case change.OpInsert:
		p.muts = append(p.muts, mutation{
			group: c.From.Group, key: c.Key, value: norm(c.From.Value),
		})
		p.key = c.Key
		p.entry = memberEntry{value: norm(c.From.Value), group: c.From.Group}
		p.entryPresent = true
	case change.OpDelete:
		p.muts = append(p.muts, mutation{
			group: c.From.Group, key: c.Key, value: norm(c.From.Value), removed: true,
		})
		p.key = c.Key
	case change.OpUpdate:
		old := v.members[c.Key]
		p.muts = append(p.muts, mutation{
			group: old.group, key: c.Key, value: old.value, removed: true,
		})
		p.muts = append(p.muts, mutation{
			group: c.To.Group, key: c.Key, value: norm(c.To.Value),
		})
		p.key = c.Key
		p.entry = memberEntry{value: norm(c.To.Value), group: c.To.Group}
		p.entryPresent = true
	}
	for _, m := range p.muts {
		p.touched[m.group] = v.ensureGroup(m.group)
	}
	return p
}

// applyPlan performs the Apply phase: fold insertions/non-extremum
// deletions incrementally, mark groups that need full recomputation.
func (v *View) applyPlan(p *plan) {
	for _, m := range p.muts {
		g := p.touched[m.group]
		mem := agg.Member{Key: m.key, Value: m.value}
		if !m.removed {
			g.members[m.key] = m.value
			for _, k := range agg.Family {
				g.aggs[k].Insert(mem)
			}
			continue
		}
		delete(g.members, m.key)
		for _, k := range agg.Family {
			a := g.aggs[k]
			if !a.DeleteIncremental(mem) {
				g.dirty[k] = true
			}
		}
	}
	if p.entryPresent {
		v.members[p.key] = p.entry
	} else {
		delete(v.members, p.key)
	}
}

// recomputePlan rebuilds exactly the dirty aggregators from the surviving
// membership of their own group only, counting each record visited.
func (v *View) recomputePlan(p *plan) {
	for _, g := range p.touched {
		if len(g.dirty) == 0 {
			continue
		}
		mems := membersOf(g)
		for k := range g.dirty {
			v.stats.TriggersByKind[k]++
			v.stats.MembersByKind[k] += int64(len(mems))
			a := g.aggs[k]
			a.Reset()
			a.Recompute(mems)
			delete(g.dirty, k)
		}
	}
}

// publishPlan removes groups that became empty and finalizes membership.
func (v *View) publishPlan(p *plan) {
	for group, g := range p.touched {
		if len(g.members) == 0 {
			delete(v.groups, group)
		}
	}
}

func membersOf(g *groupState) []agg.Member {
	out := make([]agg.Member, 0, len(g.members))
	for k, val := range g.members {
		out = append(out, agg.Member{Key: k, Value: val})
	}
	return out
}

func fingerprint(c change.Change) uint64 {
	h := fnv.New64a()
	_, _ = h.Write(change.Encode(c))
	return h.Sum64()
}
