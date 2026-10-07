package temporal

import "sort"

// This file is an intentionally simple, *independent* reference
// implementation. It answers every question by scanning the full commit log
// from the beginning up to the requested instant. It deliberately shares no
// index, treap or timeline code with Store/Snapshot: random-operation tests
// compare production traversals against this oracle, so agreement is a
// genuine cross-validation rather than two views of the same data structure.

// NaiveModel replays the canonical commit log.
type NaiveModel struct {
	log []CommitEntry
}

// NewNaiveModel returns an empty oracle.
func NewNaiveModel() *NaiveModel { return &NaiveModel{} }

// IngestLog copies the store's commit log into the oracle.
func (m *NaiveModel) IngestLog(entries []CommitEntry) {
	m.log = append(m.log[:0:0], entries...)
}

// stateAt is the oracle's whole-world projection at instant t, built by
// scanning every commit with at <= t from scratch.
type naiveState struct {
	objectType map[TypeID][]Property
	linkType   map[LinkTypeID]Cardinality
	objects    map[ObjectID]objectRecord
	alive      map[ObjectID]bool
	edges      map[edgeKey]bool
}

func (m *NaiveModel) replay(t Instant) naiveState {
	st := naiveState{
		objectType: map[TypeID][]Property{},
		linkType:   map[LinkTypeID]Cardinality{},
		objects:    map[ObjectID]objectRecord{},
		alive:      map[ObjectID]bool{},
		edges:      map[edgeKey]bool{},
	}
	for _, e := range m.log {
		if e.At > t {
			break
		}
		for id, props := range e.CreateObjectTypes {
			st.objectType[id] = props
		}
		for id, props := range e.MigrateTypes {
			st.objectType[id] = props
		}
		for id, card := range e.CreateLinkTypes {
			st.linkType[id] = card
		}
		for id, card := range e.AdjustCards {
			st.linkType[id] = card
		}
		for id, rec := range e.CreateObjects {
			st.objects[id] = rec
			st.alive[id] = true
		}
		for id, props := range e.SetProps {
			rec := st.objects[id]
			merged := cloneProps(rec.props)
			for k, v := range props {
				merged[k] = v
			}
			rec.props = merged
			st.objects[id] = rec
		}
		for _, id := range e.DeleteObjects {
			st.alive[id] = false
		}
		for _, key := range e.CreateLinks {
			st.edges[key] = true
		}
		for _, key := range e.RevokeLinks {
			st.edges[key] = false
		}
	}
	return st
}

// NaiveObject returns one object's oracle state at t.
func (m *NaiveModel) NaiveObject(t Instant, id ObjectID) ObjectState {
	st := m.replay(t)
	if !st.alive[id] {
		return ObjectState{ID: id}
	}
	rec := st.objects[id]
	return ObjectState{ID: id, Type: rec.typeID, Exists: true, Properties: cloneProps(rec.props)}
}

// NaiveLinkExists answers link existence at t by scanning the log.
func (m *NaiveModel) NaiveLinkExists(t Instant, lt LinkTypeID, src, dst ObjectID) bool {
	return m.replay(t).edges[edgeKey{linkType: lt, src: src, dst: dst}]
}

// NaiveTypeProps returns the property definition exactly covering t.
func (m *NaiveModel) NaiveTypeProps(t Instant, id TypeID) ([]Property, bool) {
	props, ok := m.replay(t).objectType[id]
	return props, ok
}

// NaiveCardinality returns the cardinality version exactly covering t.
func (m *NaiveModel) NaiveCardinality(t Instant, id LinkTypeID) (Cardinality, bool) {
	c, ok := m.replay(t).linkType[id]
	return c, ok
}

// NaiveTraverse performs a straightforward BFS against the replayed state:
// O(total committed history) per decision, deterministic output ordering
// (objects by discovery order using sorted edge order), and zero code shared
// with the production traversal.
func (m *NaiveModel) NaiveTraverse(cfg TraversalConfig) *TraversalResult {
	st := m.replay(cfg.At)
	if !st.alive[cfg.Start] {
		return nil
	}
	allow := map[LinkTypeID]bool{}
	for _, lt := range cfg.LinkTypes {
		allow[lt] = true
	}

	depthOf := map[ObjectID]int{cfg.Start: 0}
	order := []ObjectID{cfg.Start}
	var links []Link
	queue := []ObjectID{cfg.Start}
	for len(queue) > 0 {
		src := queue[0]
		queue = queue[1:]
		depth := depthOf[src]

		var out []Link
		for key, alive := range st.edges {
			if alive && key.src == src && (len(allow) == 0 || allow[key.linkType]) {
				out = append(out, Link{Type: key.linkType, Src: key.src, Dst: key.dst})
			}
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].Type != out[j].Type {
				return out[i].Type < out[j].Type
			}
			return out[i].Dst < out[j].Dst
		})
		for _, l := range out {
			links = append(links, l)
			if _, seen := depthOf[l.Dst]; !seen {
				depthOf[l.Dst] = depth + 1
				order = append(order, l.Dst)
				queue = append(queue, l.Dst)
			}
		}
	}

	objects := make([]VisitedObject, 0, len(order))
	for _, id := range order {
		rec := st.objects[id]
		objects = append(objects, VisitedObject{
			State: ObjectState{ID: id, Type: rec.typeID, Exists: true, Properties: cloneProps(rec.props)},
			Depth: depthOf[id],
		})
	}

	// The oracle mirrors the same budget semantics for comparison purposes.
	if cfg.Limits.MaxVisited >= 0 && len(objects) > cfg.Limits.MaxVisited {
		return nil
	}
	maxDepth := 0
	for _, d := range depthOf {
		if d > maxDepth {
			maxDepth = d
		}
	}
	if cfg.Limits.MaxDepth >= 0 && maxDepth > cfg.Limits.MaxDepth {
		return nil
	}
	return &TraversalResult{At: cfg.At, Objects: objects, Links: links}
}
