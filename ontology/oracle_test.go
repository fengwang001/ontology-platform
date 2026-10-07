package ontology

// This file contains an independently implemented, deliberately naive
// exhaustive oracle for snapshot extraction. It shares NO implementation code
// with Graph.Extract: it keeps plain slices of objects/links and scans every
// single object and link on every extraction (O(|V|+|E|)). It exists solely to
// cross-check the adjacency-index implementation on randomized histories.

type naiveObject struct {
	id      ObjectID
	typ     TypeName
	readers map[Principal]bool
	alive   bool
}

type naiveLink struct {
	typ    TypeName
	source ObjectID
	sink   ObjectID
	alive  bool
}

type naiveGraph struct {
	objects     map[ObjectID]*naiveObject
	links       []*naiveLink
	linkTypes   map[TypeName]LinkType
	objectTypes map[TypeName]bool
	version     uint64
}

func newNaiveGraph() *naiveGraph {
	return &naiveGraph{
		objects:     map[ObjectID]*naiveObject{},
		linkTypes:   map[TypeName]LinkType{},
		objectTypes: map[TypeName]bool{},
	}
}

func (n *naiveGraph) addObjectType(name TypeName) { n.objectTypes[name] = true }

func (n *naiveGraph) addLinkType(t LinkType) { n.linkTypes[t.Name] = t }

func (n *naiveGraph) addObject(id ObjectID, typ TypeName, readers []Principal) bool {
	if _, ok := n.objects[id]; ok {
		return false
	}
	rs := map[Principal]bool{}
	for _, r := range readers {
		rs[r] = true
	}
	n.objects[id] = &naiveObject{id: id, typ: typ, readers: rs, alive: true}
	n.version++
	return true
}

func (n *naiveGraph) removeObject(id ObjectID) bool {
	o, ok := n.objects[id]
	if !ok || !o.alive {
		return false
	}
	o.alive = false
	for _, l := range n.links {
		if l.alive && (l.source == id || l.sink == id) {
			l.alive = false
		}
	}
	n.version++
	return true
}

func (n *naiveGraph) addLink(typ TypeName, a, b ObjectID) bool {
	t, ok := n.linkTypes[typ]
	if !ok {
		return false
	}
	oa, oka := n.objects[a]
	ob, okb := n.objects[b]
	if !oka || !okb || !oa.alive || !ob.alive {
		return false
	}
	if !linkEndpointsMatch(t, oa.typ, ob.typ) {
		return false
	}
	// Canonical duplicate check for undirected links.
	for _, l := range n.links {
		if !l.alive || l.typ != typ {
			continue
		}
		if t.Direct == Undirected {
			if (l.source == a && l.sink == b) || (l.source == b && l.sink == a) {
				return false
			}
		} else if l.source == a && l.sink == b {
			return false
		}
	}
	n.links = append(n.links, &naiveLink{typ: typ, source: a, sink: b, alive: true})
	n.version++
	return true
}

func (n *naiveGraph) removeLink(typ TypeName, a, b ObjectID) bool {
	t := n.linkTypes[typ]
	for _, l := range n.links {
		if !l.alive || l.typ != typ {
			continue
		}
		match := l.source == a && l.sink == b
		if t.Direct == Undirected {
			match = match || (l.source == b && l.sink == a)
		}
		if match {
			l.alive = false
			n.version++
			return true
		}
	}
	return false
}

type naiveResult struct {
	epoch    uint64
	objects  []ObjectID
	links    []Link
	dangling []DanglingLink
	empty    bool
	err      bool
}

// extract is the brute-force reference: validates, filters by permission, then
// scans EVERY object and EVERY link in the store.
func (n *naiveGraph) extract(caller Principal, scope []ObjectID) naiveResult {
	if len(scope) == 0 {
		return naiveResult{err: true}
	}
	requested := map[ObjectID]bool{}
	for _, id := range scope {
		if !validObjectID(id) {
			return naiveResult{err: true}
		}
		requested[id] = true
	}
	if len(requested) > MaxScopeSize {
		return naiveResult{err: true}
	}

	included := map[ObjectID]bool{}
	for id := range requested {
		o, ok := n.objects[id]
		if !ok || !o.alive {
			continue
		}
		if o.readers[caller] {
			included[id] = true
		}
	}
	if len(included) == 0 {
		return naiveResult{epoch: n.version, objects: []ObjectID{}, links: []Link{}, dangling: []DanglingLink{}, empty: true}
	}

	var internal []Link
	var dangling []DanglingLink
	// Exhaustive scan over ALL links — the defining "naive" property.
	for _, l := range n.links {
		if !l.alive {
			continue
		}
		srcIn := included[l.source]
		sinkIn := included[l.sink]
		switch {
		case srcIn && sinkIn:
			internal = append(internal, Link{Type: l.typ, Source: l.source, Sink: l.sink})
		case srcIn || sinkIn:
			var local, remote ObjectID
			if srcIn {
				local, remote = l.source, l.sink
			} else {
				local, remote = l.sink, l.source
			}
			// Precedence: scope boundary first.
			src := DanglingByPermission
			if !requested[remote] {
				src = DanglingByScope
			}
			redacted := !n.objects[remote].readers[caller]
			rec := DanglingLink{
				Type:           l.typ,
				LinkDirection:  n.linkTypes[l.typ].Direct,
				LocalEnd:       local,
				RemoteEnd:      remote,
				RemoteRedacted: redacted,
				Source:         src,
			}
			if redacted {
				rec.RemoteEnd = ""
			}
			dangling = append(dangling, rec)
		}
	}

	var objs []ObjectID
	for id := range included {
		objs = append(objs, id)
	}
	sortIDs(objs)
	sortLinks(internal)
	sortDangling(dangling)
	if internal == nil {
		internal = []Link{}
	}
	if dangling == nil {
		dangling = []DanglingLink{}
	}
	return naiveResult{epoch: n.version, objects: objs, links: internal, dangling: dangling}
}

func sortIDs(ids []ObjectID) {
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j-1] > ids[j]; j-- {
			ids[j-1], ids[j] = ids[j], ids[j-1]
		}
	}
}
