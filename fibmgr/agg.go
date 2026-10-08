package fibmgr

// This file maintains the aggregation invariant:
//
// For every trie node v, cost_v(c) = min number of data plane entries
// inside v's range to reproduce the control plane function there, given
// that addresses left uncovered inside v inherit color c, satisfies
//
//	cost_v(c) = v.a        if c in v.cs
//	cost_v(c) = v.a + 1    otherwise
//
// The data plane entries stored on nodes realize cost_root(none), which
// is the minimum possible entry count.

// colorOf maps a nexthop to its color id, interning strings on demand.
func (m *Manager) colorOf(nh NextHop) uint32 {
	if nh.blackhole {
		return colorBlackhole
	}
	if id, ok := m.intern[nh.name]; ok {
		return id
	}
	id := uint32(len(m.names)) + 2
	m.intern[nh.name] = id
	m.names = append(m.names, nh.name)
	return id
}

func (m *Manager) outcomeOf(c uint32) PlaneResult {
	switch c {
	case colorNone:
		return PlaneResult{Outcome: NoRoute}
	case colorBlackhole:
		return PlaneResult{Outcome: Drop}
	default:
		return PlaneResult{Outcome: Via, Nexthop: m.names[c-2]}
	}
}

// childSummary returns the (a, cs) summary of a child slot; an absent
// child is a gap uniformly colored e and costs nothing when the
// inherited color equals e.
func childSummary(c *node, e uint32) (int, []uint32) {
	if c == nil {
		return 0, []uint32{e}
	}
	return c.a, c.cs
}

// recomputeNode recomputes v's summary from its children, marking it
// dirty when the summary changes.
func (m *Manager) recomputeNode(v *node, e uint32, depth int, parent *node) {
	stats.Nodes++
	al, cl := childSummary(v.child[0], e)
	ar, cr := childSummary(v.child[1], e)
	a, cs := combine(al, cl, ar, cr)
	if a != v.a || !equalCS(cs, v.cs) {
		m.undo.saveNode(v)
		v.a, v.cs = a, cs
		v.dirty = true
		if m.batchTop == nil || depth < m.batchTopDepth {
			m.batchTop = v
			m.batchTopDepth = depth
			m.batchTopParent = parent
		}
	}
}

// recomputeSubtree recomputes summaries for the whole subtree rooted at
// v, whose inherited control color is inh. Cost is proportional to the
// subtree size, i.e. the routes inside the affected address range.
func (m *Manager) recomputeSubtree(v *node, parent *node, inh uint32, depth int) {
	stats.Nodes++
	e := inh
	if v.hasRoute {
		e = m.colorOf(v.route)
	}
	m.undo.saveNode(v)
	v.e = e
	for b := 0; b < 2; b++ {
		if c := v.child[b]; c != nil {
			m.recomputeSubtree(c, v, e, depth+1)
		}
	}
	m.recomputeNode(v, e, depth, parent)
	v.dirtySub = v.dirty ||
		(v.child[0] != nil && v.child[0].dirtySub) ||
		(v.child[1] != nil && v.child[1].dirtySub)
}

// recomputeAncestors recomputes summaries along the ancestor path
// (stack[0] is the root, stack[i] has depth i), stopping as soon as a
// summary no longer changes.
func (m *Manager) recomputeAncestors(stack []*node) {
	for i := len(stack) - 1; i >= 0; i-- {
		u := stack[i]
		var parent *node
		if i > 0 {
			parent = stack[i-1]
		}
		m.recomputeNode(u, u.e, i, parent)
		u.dirtySub = u.dirty ||
			(u.child[0] != nil && u.child[0].dirtySub) ||
			(u.child[1] != nil && u.child[1].dirtySub)
		if !u.dirty {
			break
		}
	}
}

// fixup repairs data plane entries top-down so they realize the minimal
// count for the current summaries. v's subtree is entered with inherited
// color c. Subtrees whose summary and inherited color are unchanged are
// pruned, so the cost stays local to the update.
func (m *Manager) fixup(v *node, c uint32) {
	if v == nil {
		return
	}
	if v.solved && v.inh == c && !v.dirty && !v.dirtySub {
		return
	}
	stats.Nodes++
	e := v.e
	cl, cr := v.child[0], v.child[1]
	var csl, csr []uint32
	if cl != nil {
		csl = cl.cs
	} else {
		csl = []uint32{e}
	}
	if cr != nil {
		csr = cr.cs
	} else {
		csr = []uint32{e}
	}
	if containsCS(csl, c) || containsCS(csr, c) {
		v.hasEntry = false
	} else {
		// An entry is required (or ties with not having one); keep the
		// old color when still optimal to minimize churn.
		if !(v.hasEntry && containsCS(v.cs, v.entry)) {
			v.entry = v.cs[0]
		}
		v.hasEntry = true
	}
	nc := c
	if v.hasEntry {
		nc = v.entry
	}
	v.inh = c
	v.solved = true
	v.dirty = false
	for b := 0; b < 2; b++ {
		if ch := v.child[b]; ch != nil {
			m.fixup(ch, nc)
			if emptyNode(ch) {
				v.child[b] = nil
			}
		} else if nc != e {
			// The gap must be covered by an explicit entry.
			v.child[b] = &node{
				e:        e,
				a:        0,
				cs:       []uint32{e},
				solved:   true,
				inh:      nc,
				hasEntry: true,
				entry:    e,
			}
		}
	}
	v.dirtySub = false
}

func emptyNode(v *node) bool {
	return !v.hasRoute && !v.hasEntry && v.child[0] == nil && v.child[1] == nil
}

// count returns the minimum data plane entry count for the current
// control plane: cost_root(none), since addresses above the root are
// uncovered, i.e. implicitly no-route at no cost.
func (m *Manager) count() int {
	if m.root == nil {
		return 0
	}
	a := m.root.a
	if !containsCS(m.root.cs, colorNone) {
		a++
	}
	return a
}

func (m *Manager) lookupControl(addr uint32) PlaneResult {
	c, found := colorNone, false
	for v, d := m.root, 0; v != nil; {
		if v.hasRoute {
			c, found = m.colorOf(v.route), true
		}
		if d == 32 {
			break
		}
		v = v.child[bit(addr, d)]
		d++
	}
	if !found {
		return PlaneResult{Outcome: NoRoute}
	}
	return m.outcomeOf(c)
}

func (m *Manager) lookupData(addr uint32) PlaneResult {
	c, found := colorNone, false
	for v, d := m.root, 0; v != nil; {
		if v.hasEntry {
			c, found = v.entry, true
		}
		if d == 32 {
			break
		}
		v = v.child[bit(addr, d)]
		d++
	}
	if !found {
		return PlaneResult{Outcome: NoRoute}
	}
	return m.outcomeOf(c)
}

// collectEntries appends all data plane entries in pre-order, which is
// ascending start address and, for equal starts, ascending prefix length.
func (m *Manager) collectEntries(out *[]Entry) {
	var walk func(v *node, addr uint32, d int)
	walk = func(v *node, addr uint32, d int) {
		if v == nil {
			return
		}
		if v.hasEntry {
			r := m.outcomeOf(v.entry)
			*out = append(*out, Entry{Prefix: Prefix{Addr: addr, Len: d}, Outcome: r.Outcome, Nexthop: r.Nexthop})
		}
		if d == 32 {
			return
		}
		walk(v.child[0], addr, d+1)
		walk(v.child[1], addr|(uint32(1)<<uint(31-d)), d+1)
	}
	walk(m.root, 0, 0)
}
