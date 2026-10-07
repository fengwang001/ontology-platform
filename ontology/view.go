package ontology

import "sort"

// shadowView is the post-batch working image presented to validation hooks
// and used by cardinality evaluation. It never mutates committed state.
type shadowView struct {
	instances map[InstanceID]*Instance
	edges     map[edgeKey]bool // true=present after batch, false=removed
}

func (v *shadowView) Get(id InstanceID) *Instance {
	if inst, ok := v.instances[id]; ok && inst != nil {
		return inst.clone()
	}
	return nil
}

func (v *shadowView) Instances() []*Instance {
	ids := make([]InstanceID, 0, len(v.instances))
	for id, inst := range v.instances {
		if inst != nil {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]*Instance, 0, len(ids))
	for _, id := range ids {
		out = append(out, v.instances[id].clone())
	}
	return out
}

func (v *shadowView) HasEdge(edge Edge) bool {
	if present, ok := v.edges[edgeKey{edge.Link, edge.Source, edge.Target}]; ok {
		return present
	}
	return false
}
