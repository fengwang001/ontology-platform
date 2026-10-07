package ontology

func (st *Store) CreateInstance(id ObjectID, typ ObjectTypeName, props map[PropertyName]string) error {
	st.mu.lock()
	defer st.mu.unlock()
	if _, ok := st.schema.state.props[typ]; !ok {
		return newError(KindSourceNotFound, "object type %q not registered", typ)
	}
	if _, exists := st.state.inst[id]; exists {
		return newError(KindSourceNotFound, "instance %q already exists", id)
	}
	var errs []error
	ins := &instance{
		id:    id,
		typ:   typ,
		props: map[PropertyName]PropertyValue{},
		out:   map[LinkTypeName]map[ObjectID]struct{}{},
	}
	for p, val := range props {
		if !st.schema.hasProperty(typ, p) {
			errs = append(errs, newError(KindSourceNotFound,
				"type %q has no property %q", typ, p))
			continue
		}
		ins.props[p] = PropertyValue{Val: val, Has: true}
	}
	if err := classify(errs); err != nil {
		return err
	}
	work := st.state.clone()
	work.inst[id] = ins
	levels, err := st.reindexAllOn(work, id)
	if err != nil {
		return err
	}
	work.commitInto(st.state)
	st.log("create_instance", map[string]any{
		"id": id, "type": typ, "initial_propagation": levels,
	})
	return nil
}

func (st *Store) DeleteInstance(id ObjectID) ([]propagationLevel, error) {
	st.mu.lock()
	defer st.mu.unlock()
	ins, ok := st.state.inst[id]
	if !ok {
		return nil, newError(KindSourceNotFound, "instance %q not found", id)
	}
	work := st.state.clone()

	// 1) 收集因入边全部消失而受影响的下游实例（本实例是它们的来源目标）。
	var affected []affectedNode
	for ln, froms := range work.incoming[id] {
		lt, ok := st.schema.linkType(ln)
		if !ok {
			continue
		}
		for _, node := range st.schema.state.derivedByLink[ln] {
			if lt.SrcType != node.typ {
				continue
			}
			for from := range froms {
				affected = append(affected, affectedNode{node: node, id: from})
			}
		}
	}

	// 2) 删除全部入边。
	for ln, froms := range work.incoming[id] {
		for from := range froms {
			if src, ok := work.inst[from]; ok {
				delete(src.out[ln], id)
			}
		}
	}
	delete(work.incoming, id)

	// 3) 删除全部出边并维护对端 incoming（对端自身的派生条目不受影响）。
	for ln, tos := range ins.out {
		for to := range tos {
			if back := work.incoming[to]; back != nil {
				if set := back[ln]; set != nil {
					delete(set, id)
				}
			}
		}
	}

	// 4) 摘除被删实例自身的全部派生索引条目，它不再作为任何查询结果出现。
	for prop := range work.states[id] {
		key := propKey{ins.typ, prop}
		for val, members := range work.index[key] {
			if _, member := members[id]; member {
				delete(members, id)
				if len(members) == 0 {
					delete(work.index[key], val)
				}
			}
		}
	}
	delete(work.states, id)
	delete(work.inst, id)

	// 5) 同一单元内把依赖它的下游转为不可索引。
	levels, err := st.propagate(work, affected, "source instance deleted")
	if err != nil {
		return nil, err
	}
	work.commitInto(st.state)
	st.log("delete_instance", map[string]any{"id": id, "propagation": levels})
	return levels, nil
}

// Multi 在同一个原子单元内依次执行多个操作（用于并发交织建模与复合事务）。
func (st *Store) Multi(ops []Op) ([]propagationLevel, error) {
	st.mu.lock()
	defer st.mu.unlock()
	work := st.state.clone()
	skip := unitSkip{}
	var total []propagationLevel
	for i, op := range ops {
		var levels []propagationLevel
		var err error
		switch op.Kind {
		case OpSetAttribute:
			levels, err = st.setAttribute(work, op.ID, op.Prop, op.Val)
		case OpAddLink:
			levels, err = st.addLink(work, skip, op.Link, op.ID, op.To)
		case OpRemoveLink:
			levels, err = st.removeLink(work, skip, op.Link, op.ID, op.To)
		default:
			err = newError(KindDownstreamUpdateFailed, "unknown op kind at index %d", i)
		}
		if err != nil {
			return nil, err // 整个复合单元回滚，任何一步都不可观察。
		}
		total = append(total, levels...)
	}
	work.commitInto(st.state)
	st.log("multi", map[string]any{"ops": describeOps(ops), "propagation": total})
	return total, nil
}
