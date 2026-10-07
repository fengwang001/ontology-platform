package ontology

import "sort"

type affectedNode struct {
	node propKey
	id   ObjectID
}

// unitSkip 记录“当前原子单元内已经应用过、无需再次应用”的边，
// 键为 链接名\x00from\x00to。它保证同一单元中“加同一条边 + 删同一条边”
// 这类序列的净效果被正确计算：幂等命中也要登记，否则后续相反操作会误判。
type unitSkip map[string]bool

func edgeKey(l LinkTypeName, from, to ObjectID) string {
	return string(l) + "\x00" + string(from) + "\x00" + string(to)
}

func (st *Store) setAttribute(s *storeState, id ObjectID, prop PropertyName,
	v PropertyValue) ([]propagationLevel, error) {
	ins, ok := s.inst[id]
	if !ok {
		return nil, newError(KindSourceNotFound, "source instance %q not found", id)
	}
	if !st.schema.hasProperty(ins.typ, prop) {
		return nil, newError(KindSourceNotFound,
			"instance %q type %q has no property %q", id, ins.typ, prop)
	}
	if d, derived := st.schema.derivedIndex(ins.typ, prop); derived {
		return nil, newError(KindLinkTypeNotSupported,
			"property %q on type %q is derived via %q and cannot be written directly",
			prop, ins.typ, d.Link)
	}
	if ins.props[prop] == v {
		return nil, nil // 值未变化：不触及任何下游。
	}
	ins.props[prop] = v

	// 直接下游：以该 (类型,属性) 为取值来源的派生节点上、真实指向该实例的下游实例。
	var affected []affectedNode
	for _, down := range st.schema.state.dependents[propKey{ins.typ, prop}] {
		d := st.schema.state.derived[down]
		for from := range s.incoming[id][d.Link] {
			affected = append(affected, affectedNode{node: down, id: from})
		}
	}
	return st.propagate(s, affected, "source attribute changed")
}

func (st *Store) addLink(s *storeState, skip unitSkip, ln LinkTypeName, from, to ObjectID,
) ([]propagationLevel, error) {
	lt, ok := st.schema.linkType(ln)
	if !ok {
		return nil, newError(KindLinkTypeNotSupported, "link type %q not registered", ln)
	}
	var errs []error
	src, srcOK := s.inst[from]
	if !srcOK {
		errs = append(errs, newError(KindSourceNotFound, "source instance %q not found", from))
	} else if src.typ != lt.SrcType {
		errs = append(errs, newError(KindLinkTypeNotSupported,
			"instance %q is %q, link %q requires %q", from, src.typ, ln, lt.SrcType))
	}
	dst, dstOK := s.inst[to]
	if !dstOK {
		errs = append(errs, newError(KindSourceNotFound, "target instance %q not found", to))
	} else if dst.typ != lt.DstType {
		errs = append(errs, newError(KindLinkTypeNotSupported,
			"instance %q is %q, link %q requires %q", to, dst.typ, ln, lt.DstType))
	}
	if err := classify(errs); err != nil {
		return nil, err
	}
	if _, dup := src.out[ln][to]; dup {
		if skip != nil {
			skip[edgeKey(ln, from, to)] = true
		}
		return nil, nil // 幂等：边已存在；登记以便同单元后续操作正确判净效果。
	}
	if src.out[ln] == nil {
		src.out[ln] = map[ObjectID]struct{}{}
	}
	src.out[ln][to] = struct{}{}
	if s.incoming[to] == nil {
		s.incoming[to] = map[LinkTypeName]map[ObjectID]struct{}{}
	}
	if s.incoming[to][ln] == nil {
		s.incoming[to][ln] = map[ObjectID]struct{}{}
	}
	s.incoming[to][ln][from] = struct{}{}

	var affected []affectedNode
	for _, node := range st.schema.state.derivedByLink[ln] {
		if node.typ == lt.SrcType {
			affected = append(affected, affectedNode{node: node, id: from})
		}
	}
	return st.propagate(s, affected, "link added")
}

func (st *Store) removeLink(s *storeState, skip unitSkip, ln LinkTypeName, from, to ObjectID,
) ([]propagationLevel, error) {
	lt, ok := st.schema.linkType(ln)
	if !ok {
		return nil, newError(KindLinkTypeNotSupported, "link type %q not registered", ln)
	}
	if _, ok := s.inst[from]; !ok {
		return nil, newError(KindSourceNotFound, "source instance %q not found", from)
	}
	if _, ok := s.inst[to]; !ok {
		return nil, newError(KindSourceNotFound, "target instance %q not found", to)
	}
	if _, present := s.inst[from].out[ln][to]; !present {
		return nil, nil // 幂等：边不存在。
	}
	if skip != nil && skip[edgeKey(ln, from, to)] {
		// 这条边在本单元早些时候以幂等加边命中（单元开始前就存在），
		// 现在又被删除：删除是真实变更，清除跳过标记以确保传播照常发生。
		delete(skip, edgeKey(ln, from, to))
	}
	delete(s.inst[from].out[ln], to)
	if back := s.incoming[to]; back != nil {
		if set := back[ln]; set != nil {
			delete(set, from)
		}
	}
	var affected []affectedNode
	for _, node := range st.schema.state.derivedByLink[ln] {
		if node.typ == lt.SrcType {
			affected = append(affected, affectedNode{node: node, id: from})
		}
	}
	return st.propagate(s, affected, "link removed")
}

// reindexAllOn 在实例创建后物化它自身声明的全部派生属性初始状态。
func (st *Store) reindexAllOn(s *storeState, id ObjectID) ([]propagationLevel, error) {
	ins := s.inst[id]
	var seed []affectedNode
	for _, d := range st.sortedDerivedForType(ins.typ) {
		seed = append(seed, affectedNode{node: propKey{ins.typ, d.PropName}, id: id})
	}
	return st.propagate(s, seed, "instance created")
}

func (st *Store) sortedDerivedForType(t ObjectTypeName) []DerivedIndex {
	var out []DerivedIndex
	for k, d := range st.schema.state.derived {
		if k.typ == t {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PropName < out[j].PropName })
	return out
}

func dedupAffected(in []affectedNode) []affectedNode {
	seen := map[affectedNode]bool{}
	out := make([]affectedNode, 0, len(in))
	for _, a := range in {
		if seen[a] {
			continue
		}
		seen[a] = true
		out = append(out, a)
	}
	return out
}
