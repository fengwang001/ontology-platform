package ontology

// NaiveModel independently keeps a full instance snapshot and recomputes every
// aggregate by scanning all instances on every query.
type NaiveModel struct {
	types     map[string]ObjectType
	views     map[string]ViewSpec
	instances map[string]map[string]Instance
	lastCost  int
}

// NewNaiveModel constructs the reference model with identical declarations.
func NewNaiveModel(types []ObjectType, views []ViewSpec) *NaiveModel {
	n := &NaiveModel{
		types:     make(map[string]ObjectType),
		views:     make(map[string]ViewSpec),
		instances: make(map[string]map[string]Instance),
	}
	for _, t := range types {
		n.types[t.Name] = t
	}
	for _, w := range views {
		n.views[w.Name] = w
	}
	return n
}

// ApplyWrite mirrors an accepted write.
func (n *NaiveModel) ApplyWrite(req WriteRequest, version int64) {
	if n.instances[req.Type] == nil {
		n.instances[req.Type] = make(map[string]Instance)
	}
	n.instances[req.Type][req.Key] = Instance{
		TypeName: req.Type, Key: req.Key, Attrs: cloneAttrs(req.Attrs),
		Version: version, Deleted: false,
	}
}

// ApplyDelete mirrors an accepted delete.
func (n *NaiveModel) ApplyDelete(req DeleteRequest) {
	rec := n.instances[req.Type][req.Key]
	rec.Deleted = true
	n.instances[req.Type][req.Key] = rec
}

// Query recomputes one group by scanning every live instance.
func (n *NaiveModel) Query(view, group string) GroupResult {
	n.lastCost = 0
	spec, ok := n.views[view]
	if !ok {
		return GroupResult{View: view, Group: group, Exists: false}
	}
	res := GroupResult{View: view, Group: group, Exists: false}
	for _, bucket := range n.instances {
		for _, inst := range bucket {
			n.lastCost++ // every stored record is inspected
			if inst.Deleted {
				continue
			}
			for _, src := range spec.Sources {
				if src.Type != inst.TypeName {
					continue
				}
				g, val, contributes := contribution(spec, src, inst)
				if !contributes || g != group {
					continue
				}
				res.Exists = true
				res.Count++
				res.Value += val
			}
		}
	}
	if spec.Kind == AggCount {
		res.Value = float64(res.Count)
	}
	return res
}

// ScanCost counts the live instances inspected during the last query.
func (n *NaiveModel) ScanCost() int { return n.lastCost }

// liveCount returns the total number of live instances across all types.
func (n *NaiveModel) liveCount() int {
	total := 0
	for _, bucket := range n.instances {
		for _, inst := range bucket {
			if !inst.Deleted {
				total++
			}
		}
	}
	return total
}
