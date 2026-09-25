package view

import (
	"errors"
	"math"

	"ontology/agg"
	"ontology/change"
)

type recRef struct {
	group string
	value float64
}

type group struct {
	members  map[string]float64
	sum      agg.Summer
	min, max float64
	distinct map[float64]int
}

func (v *View) validate(c change.Change) error {
	switch c.Op {
	case change.Insert:
		if !c.HasGroup {
			return ErrNoGroup
		}
		if math.IsNaN(c.Value) {
			return ErrNaN
		}
		if _, ok := v.records[c.ID]; ok {
			return ErrDup
		}
	case change.Delete:
		if _, ok := v.records[c.ID]; !ok {
			return ErrNoRecord
		}
	case change.Update:
		if !c.HasGroup {
			return ErrNoGroup
		}
		if math.IsNaN(c.Value) {
			return ErrNaN
		}
		if _, ok := v.records[c.ID]; !ok {
			return ErrNoRecord
		}
	default:
		return errors.New("view: unknown op")
	}
	return nil
}

func (v *View) mutate(c change.Change) {
	switch c.Op {
	case change.Insert:
		v.insert(c)
	case change.Delete:
		v.remove(c.ID)
	case change.Update:
		v.remove(c.ID)
		v.insert(c)
	}
}

func (v *View) commit(c change.Change) {
	v.maxVersion, v.last, v.hasLast = c.Version, c, true
}

func (v *View) insert(c change.Change) {
	g := v.groups[c.Group]
	if g == nil {
		g = &group{members: map[string]float64{}, distinct: map[float64]int{}}
		v.groups[c.Group] = g
	}
	g.members[c.ID] = c.Value
	g.sum.Add(c.Value)
	if len(g.members) == 1 {
		g.min, g.max = c.Value, c.Value
	} else {
		if c.Value < g.min {
			g.min = c.Value
		}
		if c.Value > g.max {
			g.max = c.Value
		}
	}
	g.distinct[c.Value]++
	v.records[c.ID] = recRef{group: c.Group, value: c.Value}
}

func (v *View) remove(id string) {
	ref := v.records[id]
	g := v.groups[ref.group]
	delete(g.members, id)
	g.sum.Sub(ref.value)
	g.distinct[ref.value]--
	if g.distinct[ref.value] == 0 {
		delete(g.distinct, ref.value)
	}
	delete(v.records, id)
	if len(g.members) == 0 {
		delete(v.groups, ref.group) // 组被删空，整体消失
		return
	}
	if ref.value == g.min { // Min 删除需要成员：触发 Recompute
		v.recompute[agg.Min]++
		v.visits[agg.Min] += int64(len(g.members))
		g.min, _ = agg.MinOf(values(g.members))
	}
	if ref.value == g.max {
		v.recompute[agg.Max]++
		v.visits[agg.Max] += int64(len(g.members))
		g.max, _ = agg.MaxOf(values(g.members))
	}
}

func values(m map[string]float64) []float64 {
	out := make([]float64, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
