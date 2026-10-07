package ontology

import (
	"fmt"
	"sort"
	"strings"
)

// naiveEngine is an independently written reference implementation of the
// batch semantics. It uses one global mutex and plain maps, re-checking
// every rule inside the critical section, with deliberately different code
// shape from Store (no per-instance locks, no shadow view, global edge scans
// for cardinality). It exists solely to cross-check the real engine.
type naiveInstance struct {
	id      InstanceID
	typ     ObjectTypeName
	version Version
	props   Property
}

type naiveEngine struct {
	mu        chan struct{} // token-based global lock
	instances map[InstanceID]*naiveInstance
	edges     map[edgeKey]struct{}
	hooks     map[ObjectTypeName]ValidationHook
	links     map[LinkTypeName]*LinkType
	objects   map[ObjectTypeName]struct{}
	commitSeq int64
}

func newNaiveEngine(reg *Registry) *naiveEngine {
	e := &naiveEngine{
		mu:        make(chan struct{}, 1),
		instances: map[InstanceID]*naiveInstance{},
		edges:     map[edgeKey]struct{}{},
		hooks:     map[ObjectTypeName]ValidationHook{},
		links:     map[LinkTypeName]*LinkType{},
		objects:   map[ObjectTypeName]struct{}{},
	}
	e.mu <- struct{}{}
	for name, t := range reg.objects {
		e.objects[name] = struct{}{}
		e.hooks[name] = t.Validate
	}
	for name, t := range reg.links {
		e.links[name] = t
	}
	return e
}

type naiveVerdict struct {
	committed bool
	kind      FailureKind
}

type naiveProposed struct {
	inst  *naiveInstance
	exist bool
}

func (e *naiveEngine) apply(in BatchInput) naiveVerdict {
	<-e.mu
	defer func() { e.mu <- struct{}{} }()

	// 0. duplicates
	seen := map[InstanceID]int{}
	for i, item := range in.Items {
		if prior, ok := seen[item.ID]; ok {
			_ = prior
			return naiveVerdict{false, FailureDuplicateDecl}
		}
		seen[item.ID] = i
	}

	// Work on plain copies; nothing is installed until the end.
	next := map[InstanceID]*naiveProposed{}

	// 1. baselines, item by item
	for _, item := range in.Items {
		cur, exists := e.instances[item.ID]
		var have Version
		if exists {
			have = cur.version
		}
		if have != item.Baseline {
			return naiveVerdict{false, FailureVersionConflict}
		}
		if _, ok := e.objects[item.Type]; !ok {
			return naiveVerdict{false, FailureValidationRejected}
		}
		var p *naiveInstance
		if item.Create {
			if exists {
				return naiveVerdict{false, FailureValidationRejected}
			}
			p = &naiveInstance{id: item.ID, typ: item.Type, version: 1, props: cloneProperties(item.Properties)}
		} else {
			if !exists {
				return naiveVerdict{false, FailureValidationRejected}
			}
			if cur.typ != item.Type {
				return naiveVerdict{false, FailureValidationRejected}
			}
			p = &naiveInstance{id: item.ID, typ: item.Type, version: cur.version + 1, props: cloneProperties(item.Properties)}
		}
		next[item.ID] = &naiveProposed{inst: p, exist: exists}
	}

	// edge overlay
	overlay := map[edgeKey]bool{}
	for _, item := range in.Items {
		for _, d := range item.LinkDeltas {
			k := edgeKey{d.Edge.Link, d.Edge.Source, d.Edge.Target}
			if v, ok := overlay[k]; ok && v != d.Add {
				return naiveVerdict{false, FailureValidationRejected}
			}
			overlay[k] = d.Add
		}
	}

	image := func(id InstanceID) *naiveInstance {
		if p, ok := next[id]; ok {
			return p.inst
		}
		return e.instances[id]
	}
	edgeExists := func(k edgeKey) bool {
		if v, ok := overlay[k]; ok {
			return v
		}
		_, ok := e.edges[k]
		return ok
	}

	for _, item := range in.Items {
		for _, d := range item.LinkDeltas {
			if !d.Add {
				continue
			}
			lt, ok := e.links[d.Edge.Link]
			if !ok {
				return naiveVerdict{false, FailureValidationRejected}
			}
			src, dst := image(d.Edge.Source), image(d.Edge.Target)
			if src == nil || dst == nil || src.typ != lt.Source || dst.typ != lt.Target {
				return naiveVerdict{false, FailureValidationRejected}
			}
		}
	}

	// 2. hooks against the final image
	view := &naiveView{e: e, next: next, edgeExists: edgeExists}
	for _, item := range in.Items {
		if hook, ok := e.hooks[item.Type]; ok && hook.Run != nil {
			inst := image(item.ID)
			if hook.Run(&Instance{ID: inst.id, Type: inst.typ, Version: inst.version, Properties: cloneProperties(inst.props)}, view) != "" {
				return naiveVerdict{false, FailureValidationRejected}
			}
		}
	}

	// 3. cardinality by global scans (deliberately naive)
	for _, lt := range e.links {
		degree := map[InstanceID]int{}
		for k := range overlay {
			if k.link != lt.Name {
				continue
			}
			if edgeExists(k) {
				degree[k.source]++
				degree[k.target]++
			}
		}
		for k := range e.edges {
			if k.link != lt.Name {
				continue
			}
			if _, overlaid := overlay[k]; overlaid {
				continue
			}
			degree[k.source]++
			degree[k.target]++
		}
		for endpoint := range degree {
			if lt.SrcMax > 0 {
				out := 0
				for k := range e.edges {
					if k.link == lt.Name && k.source == endpoint {
						if _, overlaid := overlay[k]; overlaid {
							if overlay[k] {
								out++
							}
							continue
						}
						out++
					}
				}
				for k, add := range overlay {
					if add && k.link == lt.Name && k.source == endpoint {
						if _, base := e.edges[k]; !base {
							out++
						}
					}
				}
				if out > int(lt.SrcMax) {
					return naiveVerdict{false, FailureCardinality}
				}
			}
			if lt.DstMax > 0 {
				inDeg := 0
				for k := range e.edges {
					if k.link == lt.Name && k.target == endpoint {
						if _, overlaid := overlay[k]; overlaid {
							if overlay[k] {
								inDeg++
							}
							continue
						}
						inDeg++
					}
				}
				for k, add := range overlay {
					if add && k.link == lt.Name && k.target == endpoint {
						if _, base := e.edges[k]; !base {
							inDeg++
						}
					}
				}
				if inDeg > int(lt.DstMax) {
					return naiveVerdict{false, FailureCardinality}
				}
			}
		}
	}

	// 4. commit
	for _, item := range in.Items {
		p := next[item.ID].inst
		e.instances[item.ID] = p
	}
	for k, add := range overlay {
		if add {
			e.edges[k] = struct{}{}
		} else {
			delete(e.edges, k)
		}
	}
	e.commitSeq++
	return naiveVerdict{true, FailureNone}
}

type naiveView struct {
	e          *naiveEngine
	next       map[InstanceID]*naiveProposed
	edgeExists func(edgeKey) bool
}

func (v *naiveView) Get(id InstanceID) *Instance {
	if p, ok := v.next[id]; ok {
		return &Instance{ID: p.inst.id, Type: p.inst.typ, Version: p.inst.version, Properties: cloneProperties(p.inst.props)}
	}
	if cur, ok := v.e.instances[id]; ok {
		return &Instance{ID: cur.id, Type: cur.typ, Version: cur.version, Properties: cloneProperties(cur.props)}
	}
	return nil
}

func (v *naiveView) Instances() []*Instance {
	ids := make([]InstanceID, 0)
	for id := range v.next {
		ids = append(ids, id)
	}
	for id := range v.e.instances {
		if _, ok := v.next[id]; !ok {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]*Instance, 0, len(ids))
	for _, id := range ids {
		out = append(out, v.Get(id))
	}
	return out
}

func (v *naiveView) HasEdge(edge Edge) bool {
	return v.edgeExists(edgeKey{edge.Link, edge.Source, edge.Target})
}

func (e *naiveEngine) digest() string {
	ids := make([]InstanceID, 0, len(e.instances))
	for id := range e.instances {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var b strings.Builder
	for _, id := range ids {
		c := e.instances[id]
		keys := make([]string, 0, len(c.props))
		for k := range c.props {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Fprintf(&b, "%s:%s:%d:", id, c.typ, c.version)
		for _, k := range keys {
			fmt.Fprintf(&b, "%s=%v;", k, c.props[k])
		}
		b.WriteString("|")
	}
	keys := make([]edgeKey, 0, len(e.edges))
	for k := range e.edges {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].link != keys[j].link {
			return keys[i].link < keys[j].link
		}
		if keys[i].source != keys[j].source {
			return keys[i].source < keys[j].source
		}
		return keys[i].target < keys[j].target
	})
	for _, k := range keys {
		fmt.Fprintf(&b, "%s:%s->%s;", k.link, k.source, k.target)
	}
	return b.String()
}

func storeDigest(s *Store) string {
	s.locksMu.Lock()
	ids := make([]InstanceID, 0, len(s.instances))
	for id := range s.instances {
		ids = append(ids, id)
	}
	s.locksMu.Unlock()

	snap := s.Snapshot(ids)
	sortIDs := append([]InstanceID(nil), ids...)
	sort.Slice(sortIDs, func(i, j int) bool { return sortIDs[i] < sortIDs[j] })

	s.tabMu.RLock()
	edges := make([]edgeKey, 0, len(s.edges))
	for k := range s.edges {
		edges = append(edges, k)
	}
	s.tabMu.RUnlock()

	var b strings.Builder
	for _, id := range sortIDs {
		c := snap[id]
		keys := make([]string, 0, len(c.Properties))
		for k := range c.Properties {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Fprintf(&b, "%s:%s:%d:", id, c.Type, c.Version)
		for _, k := range keys {
			fmt.Fprintf(&b, "%s=%v;", k, c.Properties[k])
		}
		b.WriteString("|")
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].link != edges[j].link {
			return edges[i].link < edges[j].link
		}
		if edges[i].source != edges[j].source {
			return edges[i].source < edges[j].source
		}
		return edges[i].target < edges[j].target
	})
	for _, k := range edges {
		fmt.Fprintf(&b, "%s:%s->%s;", k.link, k.source, k.target)
	}
	return b.String()
}
