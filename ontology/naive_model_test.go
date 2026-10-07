package ontology

import "sort"

// naiveModel is the independent reference implementation: one global mutex,
// batches applied strictly serially. It deliberately shares none of the
// platform's locking/indexing machinery.
type naiveModel struct {
	objects map[TypeName]*ObjectType
	links   map[TypeName]*LinkType
	inst    map[InstanceID]nInst
	linkSet map[linkKey]struct{}
	tick    int64
}

type nInst struct {
	typ     TypeName
	version int64
	props   Properties
}

func newNaive(p *Platform) *naiveModel {
	p.mu.Lock()
	defer p.mu.Unlock()
	m := &naiveModel{
		objects: map[TypeName]*ObjectType{},
		links:   map[TypeName]*LinkType{},
		inst:    map[InstanceID]nInst{},
		linkSet: map[linkKey]struct{}{},
	}
	for k, v := range p.objectTypes {
		m.objects[k] = v
	}
	for k, v := range p.linkTypes {
		m.links[k] = v
	}
	for k, v := range p.instances {
		m.inst[k] = nInst{typ: v.typ, version: v.version, props: cloneProps(v.props)}
	}
	for k := range p.links {
		m.linkSet[k] = struct{}{}
	}
	m.tick = p.tick
	return m
}

type naiveResult struct {
	id      string
	ok      bool
	failure FailureClass
	tick    int64
}

// emptyNaive returns a naive model with the platform's schema but no data,
// suitable for replaying a journal from the beginning.
func emptyNaive(p *Platform) *naiveModel {
	p.mu.Lock()
	defer p.mu.Unlock()
	m := &naiveModel{
		objects: map[TypeName]*ObjectType{},
		links:   map[TypeName]*LinkType{},
		inst:    map[InstanceID]nInst{},
		linkSet: map[linkKey]struct{}{},
	}
	for k, v := range p.objectTypes {
		m.objects[k] = v
	}
	for k, v := range p.linkTypes {
		m.links[k] = v
	}
	return m
}

func (m *naiveModel) apply(b Batch) naiveResult {
	seen := map[InstanceID]int{}
	for i, op := range b.Ops {
		if _, dup := seen[op.Instance]; dup {
			return naiveResult{b.ID, false, FailureDuplicateWrite, 0}
		}
		seen[op.Instance] = i
	}
	linkAdd := map[linkKey]bool{}
	for _, lop := range b.Links {
		k := normalizeLink(lop.Link, lop.A, lop.B)
		if add, dup := linkAdd[k]; dup && add != lop.Add {
			return naiveResult{b.ID, false, FailureDuplicateWrite, 0}
		}
		linkAdd[k] = lop.Add
	}
	for _, op := range b.Ops {
		var have int64
		if in, ok := m.inst[op.Instance]; ok {
			have = in.version
		}
		if have != op.BaseVersion {
			return naiveResult{b.ID, false, FailureVersionConflict, 0}
		}
	}
	for _, op := range b.Ops {
		ot, ok := m.objects[op.Type]
		if !ok {
			return naiveResult{b.ID, false, FailureHookRejected, 0}
		}
		if old, ok := m.inst[op.Instance]; ok && old.typ != op.Type {
			return naiveResult{b.ID, false, FailureHookRejected, 0}
		}
		var cur, prop Properties
		if old, ok := m.inst[op.Instance]; ok {
			cur = cloneProps(old.props)
		}
		if op.Props != nil {
			prop = cloneProps(op.Props)
		}
		if ot.Hook != nil && ot.Hook(cur, prop) != nil {
			return naiveResult{b.ID, false, FailureHookRejected, 0}
		}
	}
	finalInst := map[InstanceID]nInst{}
	for id, in := range m.inst {
		finalInst[id] = in
	}
	for _, op := range b.Ops {
		if op.Props == nil {
			delete(finalInst, op.Instance)
		} else {
			v := int64(1)
			if old, ok := finalInst[op.Instance]; ok {
				v = old.version + 1
			}
			finalInst[op.Instance] = nInst{typ: op.Type, version: v, props: cloneProps(op.Props)}
		}
	}
	finalLinks := map[linkKey]struct{}{}
	for k := range m.linkSet {
		finalLinks[k] = struct{}{}
	}
	for _, lop := range b.Links {
		k := normalizeLink(lop.Link, lop.A, lop.B)
		if lop.Add {
			finalLinks[k] = struct{}{}
		} else {
			delete(finalLinks, k)
		}
	}
	touched := map[TypeName]struct{}{}
	for _, lop := range b.Links {
		touched[lop.Link] = struct{}{}
	}
	for _, op := range b.Ops {
		if op.Props != nil {
			continue
		}
		for k := range finalLinks {
			if k.a == op.Instance || k.b == op.Instance {
				touched[k.link] = struct{}{}
			}
		}
	}
	type rc struct{ a, b int }
	deg := map[InstanceID]map[TypeName]*rc{}
	check := map[InstanceID]struct{}{}
	var keys []linkKey
	for k := range finalLinks {
		if _, ok := touched[k.link]; ok {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].link != keys[j].link {
			return keys[i].link < keys[j].link
		}
		if keys[i].a != keys[j].a {
			return keys[i].a < keys[j].a
		}
		return keys[i].b < keys[j].b
	})
	for _, k := range keys {
		lt, ok := m.links[k.link]
		if !ok {
			return naiveResult{b.ID, false, FailureCardinality, 0}
		}
		ea, aok := finalInst[k.a]
		eb, bok := finalInst[k.b]
		if !aok || !bok {
			return naiveResult{b.ID, false, FailureCardinality, 0}
		}
		mAB := ea.typ == lt.LeftType && eb.typ == lt.RightType
		mBA := ea.typ == lt.RightType && eb.typ == lt.LeftType
		if !mAB && !mBA {
			return naiveResult{b.ID, false, FailureCardinality, 0}
		}
		check[k.a] = struct{}{}
		check[k.b] = struct{}{}
		if deg[k.a] == nil {
			deg[k.a] = map[TypeName]*rc{}
		}
		if deg[k.b] == nil {
			deg[k.b] = map[TypeName]*rc{}
		}
		if deg[k.a][k.link] == nil {
			deg[k.a][k.link] = &rc{}
		}
		if deg[k.b][k.link] == nil {
			deg[k.b][k.link] = &rc{}
		}
		switch {
		case lt.LeftType == lt.RightType:
			deg[k.a][k.link].a++
			deg[k.b][k.link].a++
		case mAB:
			deg[k.a][k.link].a++
			deg[k.b][k.link].b++
		default:
			deg[k.a][k.link].b++
			deg[k.b][k.link].a++
		}
	}
	for _, lop := range b.Links {
		check[lop.A] = struct{}{}
		check[lop.B] = struct{}{}
	}
	var ids []InstanceID
	for id := range check {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		in, exists := finalInst[id]
		var lts []TypeName
		for t := range touched {
			lts = append(lts, t)
		}
		sort.Slice(lts, func(i, j int) bool { return lts[i] < lts[j] })
		for _, name := range lts {
			lt, ok := m.links[name]
			if !ok {
				continue
			}
			var n int
			var bound Cardinality
			switch {
			case lt.LeftType == lt.RightType && exists && in.typ == lt.LeftType:
				if c := deg[id][name]; c != nil {
					n = c.a
				}
				bound = lt.CardA
			case exists && in.typ == lt.LeftType:
				if c := deg[id][name]; c != nil {
					n = c.a
				}
				bound = lt.CardA
			case exists && in.typ == lt.RightType:
				if c := deg[id][name]; c != nil {
					n = c.b
				}
				bound = lt.CardB
			default:
				continue
			}
			if !within(n, bound) {
				return naiveResult{b.ID, false, FailureCardinality, 0}
			}
		}
	}
	m.inst = finalInst
	m.linkSet = finalLinks
	m.tick++
	return naiveResult{b.ID, true, FailureNone, m.tick}
}

func (m *naiveModel) snapshot() map[InstanceID]nInst {
	out := make(map[InstanceID]nInst, len(m.inst))
	for k, v := range m.inst {
		v.props = cloneProps(v.props)
		out[k] = v
	}
	return out
}

func (m *naiveModel) linksSnapshot() map[linkKey]struct{} {
	out := make(map[linkKey]struct{}, len(m.linkSet))
	for k := range m.linkSet {
		out[k] = struct{}{}
	}
	return out
}
