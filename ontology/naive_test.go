package ontology

// naiveGraph 是独立实现的朴素参考模型：
// 每次查询都对固定长度路径做全量枚举（带 visited 之外的有界深度展开，
// 但终点按“实例集合”去重），不保留任何增量状态。
type naiveGraph struct {
	types    map[string]struct{}
	linkSigs map[string][]linkSig
	objType  map[string]string
	attrs    map[string]map[string]int64
	edges    map[string]map[string]map[string]int64
	views    map[string]ViewSpec
}

func newNaive() *naiveGraph {
	return &naiveGraph{
		types:    map[string]struct{}{},
		linkSigs: map[string][]linkSig{},
		objType:  map[string]string{},
		attrs:    map[string]map[string]int64{},
		edges:    map[string]map[string]map[string]int64{},
		views:    map[string]ViewSpec{},
	}
}

func (m *naiveGraph) addType(t string) { m.types[t] = struct{}{} }

func (m *naiveGraph) addLinkType(name, s, d string) {
	m.linkSigs[name] = append(m.linkSigs[name], linkSig{srcType: s, dstType: d})
}

func (m *naiveGraph) create(id, typ string, attrs map[string]int64) {
	m.objType[id] = typ
	a := map[string]int64{}
	for k, v := range attrs {
		a[k] = v
	}
	m.attrs[id] = a
}

func (m *naiveGraph) setAttr(id, attr string, v int64) {
	if m.attrs[id] == nil {
		m.attrs[id] = map[string]int64{}
	}
	m.attrs[id][attr] = v
}

func (m *naiveGraph) addEdge(link, s, d string) {
	if m.edges[link] == nil {
		m.edges[link] = map[string]map[string]int64{}
	}
	if m.edges[link][s] == nil {
		m.edges[link][s] = map[string]int64{}
	}
	m.edges[link][s][d]++
}

func (m *naiveGraph) removeEdge(link, s, d string) {
	if m.edges[link][s][d] <= 1 {
		delete(m.edges[link][s], d)
	} else {
		m.edges[link][s][d]--
	}
}

func (m *naiveGraph) register(spec ViewSpec) { m.views[spec.Name] = spec }

// reachableEnds 用逐层集合展开（深度恰为路径长度）求 start 可达的终点实例集合。
// 集合语义天然去重：同一终点无论经多少条中间路径到达，只出现一次。
func (m *naiveGraph) reachableEnds(spec ViewSpec, start string) map[string]struct{} {
	p := spec.Path
	cur := map[string]struct{}{start: {}}
	for k := 0; k < len(p.Links); k++ {
		lname := p.Links[k]
		next := map[string]struct{}{}
		for u := range cur {
			for v := range m.edges[lname][u] {
				t := m.objType[v]
				if contains(p.Types[k+1], t) {
					next[v] = struct{}{}
				}
			}
		}
		cur = next
	}
	return cur
}

func (m *naiveGraph) query(view, start string) (present bool, val int64) {
	spec := m.views[view]
	ends := m.reachableEnds(spec, start)
	for e := range ends {
		v, ok := m.attrs[e][spec.Attr]
		if !ok {
			continue
		}
		if !present || v > val {
			present = true
			val = v
		}
	}
	return present, val
}

func (m *naiveGraph) startIDs(spec ViewSpec) []string {
	var out []string
	for id, t := range m.objType {
		if contains(spec.Path.Types[0], t) {
			out = append(out, id)
		}
	}
	return out
}
