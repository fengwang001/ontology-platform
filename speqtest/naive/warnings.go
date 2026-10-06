package naive

import "sort"

// Warnings 朴素实现：线性扫描全部对象与全部挂接关系。
func (m *Model) Warnings(date int) []Warn {
	alive := func(o *O) bool { return !o.Scrap && !o.Seal && !o.Dis }
	near := func(o *O) bool {
		c := m.Cats[o.Cat]
		return alive(o) && date <= o.Exp && o.Exp <= date+c.WarnLead
	}

	type agg struct {
		kind    Kind
		sort    int
		self    bool
		trig    []string
		selfExp int
	}
	aggs := map[string]*agg{}
	get := func(id string, k Kind, exp int) *agg {
		a := aggs[id]
		if a == nil {
			a = &agg{kind: k, sort: exp, selfExp: exp}
			aggs[id] = a
		}
		return a
	}

	var ids []string
	for id, o := range m.Objs {
		ids = append(ids, id)
		if !near(o) {
			continue
		}
		get(id, o.Kind, o.Exp).self = true
		if (o.Kind == KSV || o.Kind == KPG) && m.HostOf[id] != "" {
			did := m.HostOf[id]
			d := m.Objs[did]
			if d != nil && alive(d) {
				a := get(did, KDevice, d.Exp)
				a.trig = append(a.trig, id)
				if o.Exp < a.sort {
					a.sort = o.Exp
				}
			}
		}
	}

	out := []Warn{}
	for id, a := range aggs {
		if a.kind == KDevice && !a.self && len(a.trig) == 0 {
			continue
		}
		if !a.self && a.kind == KDevice {
			a.sort = minList(a.trig, m)
		}
		sort.Strings(a.trig)
		out = append(out, Warn{ID: id, Kind: a.kind, Exp: a.selfExp, Sort: a.sort, Trig: a.trig})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sort != out[j].Sort {
			return out[i].Sort < out[j].Sort
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func minList(ids []string, m *Model) int {
	minv := m.Objs[ids[0]].Exp
	for _, id := range ids[1:] {
		if e := m.Objs[id].Exp; e < minv {
			minv = e
		}
	}
	return minv
}
