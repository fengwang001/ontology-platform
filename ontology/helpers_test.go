package ontology

// testDecider 用矩阵驱动授权，结构上同时可导出为 naive.Policy。
type testDecider struct {
	invisible map[InstanceID]bool
	// allow[id][op][depth]
	allowMap map[InstanceID]map[Operation]map[int]bool
}

func newTestDecider() *testDecider {
	return &testDecider{
		invisible: map[InstanceID]bool{},
		allowMap:  map[InstanceID]map[Operation]map[int]bool{},
	}
}

func (d *testDecider) setInvisible(id InstanceID) { d.invisible[id] = true }

func (d *testDecider) allow(id InstanceID, op Operation, depth int, v bool) {
	if d.allowMap[id] == nil {
		d.allowMap[id] = map[Operation]map[int]bool{}
	}
	if d.allowMap[id][op] == nil {
		d.allowMap[id][op] = map[int]bool{}
	}
	d.allowMap[id][op][depth] = v
}

// allowAll 让某实例在所有深度对给定操作放行（用一个大但有限的深度窗口）。
func (d *testDecider) allowAll(id InstanceID, op Operation) {
	for lvl := 0; lvl <= 32; lvl++ {
		d.allow(id, op, lvl, true)
	}
}

func (d *testDecider) Visible(_ SubjectID, inst Instance, _ int) bool {
	return !d.invisible[inst.ID]
}

func (d *testDecider) Allowed(_ SubjectID, inst Instance, op Operation, depth int) (bool, bool) {
	if byOp, ok := d.allowMap[inst.ID]; ok {
		if m, ok := byOp[op]; ok {
			if v, has := m[depth]; has {
				return v, false
			}
		}
	}
	return false, true // 弃权
}

func mkStore(insts []Instance, edges []Edge) *MemStore {
	st := NewMemStore()
	for _, in := range insts {
		if in.Attrs == nil {
			in.Attrs = map[string]string{}
		}
		st.AddInstance(in)
	}
	for _, e := range edges {
		st.AddEdge(e)
	}
	return st
}

func inst(id string, t string) Instance {
	return Instance{ID: InstanceID(id), Type: ObjectTypeID(t), Version: 1, Attrs: map[string]string{}}
}

func edge(lt, from, to string) Edge {
	return Edge{LinkType: LinkTypeID(lt), From: InstanceID(from), To: InstanceID(to)}
}

func baseAction(target string) ActionDeclaration {
	return ActionDeclaration{
		Name:    "act",
		Subject: "s1",
		Direct: []DirectOp{
			{Op: OpUpdate, Type: "doc", Target: InstanceID(target), NewAttrs: map[string]string{"a": "1"}},
		},
		Cascades: []CascadeRule{
			{LinkType: "rel", Outgoing: true, Effect: OpUpdate},
		},
		MaxDepth:      3,
		InvisibleMode: RejectOnInvisible,
		Merge:         MergeAll,
	}
}
