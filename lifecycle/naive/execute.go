package naive

import "fmt"

func (m *Model) snapshot() (map[InstanceID]*inst, map[InstanceID]map[LinkType]map[InstanceID]bool) {
	ci := map[InstanceID]*inst{}
	for id, in := range m.insts {
		cp := &inst{typ: in.typ, state: in.state, clock: in.clock, attrs: map[AttrKey]AttrValue{}}
		for k, v := range in.attrs {
			cp.attrs[k] = v
		}
		ci[id] = cp
	}
	cl := map[InstanceID]map[LinkType]map[InstanceID]bool{}
	for s, byLink := range m.links {
		cl[s] = map[LinkType]map[InstanceID]bool{}
		for lk, dsts := range byLink {
			cl[s][lk] = map[InstanceID]bool{}
			for d := range dsts {
				cl[s][lk][d] = true
			}
		}
	}
	return ci, cl
}

func (m *Model) State(id InstanceID) (State, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	in, ok := m.insts[id]
	if !ok {
		return "", false
	}
	return in.state, true
}

func (m *Model) Attr(id InstanceID, key AttrKey) (AttrValue, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	in, ok := m.insts[id]
	if !ok {
		return nil, false
	}
	v, ok := in.attrs[key]
	return v, ok
}

func (m *Model) Linked(src InstanceID, link LinkType, dst InstanceID) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.links[src] != nil && m.links[src][link] != nil && m.links[src][link][dst]
}

func (m *Model) SetAttrs(id InstanceID, ops ...AttrOp) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	in := m.insts[id]
	if in == nil {
		return ErrUndeclared
	}
	if inStates(in.state, m.types[in.typ].Terminals) {
		return ErrTerminal
	}
	for _, op := range ops {
		if op.Op == "set" {
			in.attrs[op.Key] = op.Value
		} else {
			cur, _ := in.attrs[op.Key].(int64)
			n, _ := op.Value.(int64)
			in.attrs[op.Key] = cur + n
		}
	}
	m.clock++
	return 0
}

func (m *Model) ModifyLinks(src InstanceID, ops ...LinkOp) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	in := m.insts[src]
	if in == nil {
		return ErrUndeclared
	}
	isTerm := inStates(in.state, m.types[in.typ].Terminals)
	for _, op := range ops {
		if m.insts[op.Target] == nil {
			return ErrUndeclared
		}
		if isTerm && op.Op == "add" {
			return ErrTerminal
		}
	}
	for _, op := range ops {
		ensureLink(m.links, src, op.Link)
		if op.Op == "add" {
			m.links[src][op.Link][op.Target] = true
		} else {
			delete(m.links[src][op.Link], op.Target)
		}
	}
	m.clock++
	return 0
}

func (m *Model) logf(r Request, o Outcome) {
	if m.Log == nil {
		return
	}
	m.logSeq++
	status := "ACCEPT"
	if o.Code != 0 {
		status = fmt.Sprintf("REJECT(%d)", o.Code)
	}
	m.Log(fmt.Sprintf("[naive %06d] target=%s rule=%s pri=%d => %s %s",
		m.logSeq, r.Instance, r.Rule, r.Priority, status, o.Detail))
}
