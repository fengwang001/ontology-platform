package derived

import "sort"

// NaiveModel is an independent reference oracle. It keeps only raw objects,
// raw link triples and declarations; it holds no adjacency indexes and never
// reads the Store's caches. Every observation recomputes from scratch by
// directly walking links, so equality between Store and NaiveModel is
// evidence that the indexed subsystem matches the mathematical definition.
type NaiveModel struct {
	objects      map[string]*Object
	links        map[linkKey]struct{}
	declarations map[string]*Declaration
}

func NewNaiveModel() *NaiveModel {
	return &NaiveModel{
		objects:      map[string]*Object{},
		links:        map[linkKey]struct{}{},
		declarations: map[string]*Declaration{},
	}
}

func (n *NaiveModel) AddObject(obj Object) {
	props := map[string]Value{}
	for k, v := range obj.Properties {
		props[k] = v
	}
	n.objects[obj.ID] = &Object{ID: obj.ID, Type: obj.Type, Properties: props}
}

func (n *NaiveModel) AddDeclaration(d Declaration) {
	cp := d
	n.declarations[d.Name] = &cp
}

func (n *NaiveModel) SetProperty(objID, prop string, val Value) {
	if o, ok := n.objects[objID]; ok {
		o.Properties[prop] = val
	}
}

// AddLink mirrors a committed store add. rejected links (cycle or otherwise)
// are simply not forwarded by the test driver.
func (n *NaiveModel) AddLink(from, to, typ string) {
	n.links[linkKey{from, typ, to}] = struct{}{}
}

func (n *NaiveModel) DeleteLink(from, to, typ string) {
	delete(n.links, linkKey{from, typ, to})
}

func (n *NaiveModel) DeleteObject(id string) {
	delete(n.objects, id)
	for k := range n.links {
		if k.from == id || k.to == id {
			delete(n.links, k)
		}
	}
}

// walk targets of typ links leaving id, sorted and deduplicated.
func (n *NaiveModel) walk(id, typ string) []string {
	var out []string
	for k := range n.links {
		if k.from == id && k.typ == typ {
			out = append(out, k.to)
		}
	}
	return distinctSorted(append([]string(nil), out...))
}

type naiveRead struct {
	state EntryState
	keys  []Value
}

// read implements the spec literally, with an explicit depth guard that
// cannot legitimately trigger because cyclic links are refused.
func (n *NaiveModel) read(d *Declaration, id string, depth int) naiveRead {
	if depth > 2*len(n.declarations)+2 {
		return naiveRead{state: StateSourceGone}
	}
	ts := n.walk(id, d.LinkType)
	if len(ts) == 0 {
		return naiveRead{state: StateNoLink}
	}
	if d.RequireUnique && len(ts) > 1 {
		return naiveRead{state: StateNotUnique}
	}
	var keys []Value
	gone, noLink := false, false
	for _, t := range ts {
		obj := n.objects[t]
		if obj == nil {
			gone = true
			continue
		}
		if sub, chained := n.declarations[d.SourceProperty]; chained {
			r := n.read(sub, t, depth+1)
			if r.state == StateIndexed {
				keys = append(keys, r.keys...)
			} else if r.state == StateSourceGone {
				gone = true
			} else if r.state == StateNoLink {
				noLink = true
			}
			continue
		}
		if v, ok := obj.Properties[d.SourceProperty]; ok {
			keys = append(keys, v)
		}
	}
	if len(keys) > 0 {
		return naiveRead{state: StateIndexed, keys: distinctSorted(keys)}
	}
	if gone {
		return naiveRead{state: StateSourceGone}
	}
	if noLink {
		return naiveRead{state: StateNoLink}
	}
	return naiveRead{state: StateNoValue}
}

// Entry recomputes one entry from raw state.
func (n *NaiveModel) Entry(declName, objID string) (Entry, bool) {
	d, ok := n.declarations[declName]
	if !ok || n.objects[objID] == nil {
		return Entry{}, false
	}
	r := n.read(d, objID, 0)
	return Entry{Declaration: declName, ObjectID: objID, State: r.state, Keys: r.keys}, true
}

// AllEntries recomputes every entry for every existing object.
func (n *NaiveModel) AllEntries() []Entry {
	var out []Entry
	for _, d := range n.declarations {
		for id := range n.objects {
			if n.objects[id].Type == d.DownstreamType {
				r := n.read(d, id, 0)
				out = append(out, Entry{Declaration: d.Name, ObjectID: id,
					State: r.state, Keys: r.keys})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Declaration != out[j].Declaration {
			return out[i].Declaration < out[j].Declaration
		}
		return out[i].ObjectID < out[j].ObjectID
	})
	return out
}

// Lookup is the naive key lookup.
func (n *NaiveModel) Lookup(declName string, key Value) []string {
	d, ok := n.declarations[declName]
	if !ok {
		return nil
	}
	var out []string
	for id, obj := range n.objects {
		if obj.Type != d.DownstreamType {
			continue
		}
		r := n.read(d, id, 0)
		if r.state == StateIndexed {
			for _, k := range r.keys {
				if k == key {
					out = append(out, id)
					break
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

// DownstreamSet is the independent oracle for "exactly N touch points":
// distinct instances whose entry for any declaration depends on base property
// prop at sourceID, directly or transitively. Computed by dependency walk
// over raw link triples.
func (n *NaiveModel) DownstreamSet(sourceID, prop string) map[string]struct{} {
	affected := map[string]struct{}{}
	type nd struct{ id, prop string }
	visited := map[nd]struct{}{{sourceID, prop}: {}}
	queue := []nd{{sourceID, prop}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, d := range n.declarations {
			if d.SourceProperty != cur.prop {
				continue
			}
			for k := range n.links {
				if k.typ != d.LinkType || k.to != cur.id {
					continue
				}
				up := k.from
				if obj := n.objects[up]; obj == nil || obj.Type != d.DownstreamType {
					continue
				}
				affected[up] = struct{}{}
				next := nd{up, d.Name}
				if _, seen := visited[next]; !seen {
					visited[next] = struct{}{}
					queue = append(queue, next)
				}
			}
		}
	}
	return affected
}

// DownstreamCount is the size of DownstreamSet.
func (n *NaiveModel) DownstreamCount(sourceID, prop string) int {
	return len(n.DownstreamSet(sourceID, prop))
}
