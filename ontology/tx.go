package ontology

func (st *Store) SetAttribute(id ObjectID, prop PropertyName, v PropertyValue) ([]propagationLevel, error) {
	st.mu.lock()
	defer st.mu.unlock()
	work := st.state.clone()
	levels, err := st.setAttribute(work, id, prop, v)
	if err != nil {
		return nil, err // 工作副本被丢弃：属性写入整体不生效。
	}
	work.commitInto(st.state)
	st.log("set_attribute", map[string]any{
		"id": id, "property": prop, "value": v, "propagation": levels,
	})
	return levels, nil
}

func (st *Store) AddLink(link LinkTypeName, from, to ObjectID) ([]propagationLevel, error) {
	st.mu.lock()
	defer st.mu.unlock()
	work := st.state.clone()
	levels, err := st.addLink(work, nil, link, from, to)
	if err != nil {
		return nil, err
	}
	work.commitInto(st.state)
	st.log("add_link", map[string]any{
		"link": link, "from": from, "to": to, "propagation": levels,
	})
	return levels, nil
}

func (st *Store) RemoveLink(link LinkTypeName, from, to ObjectID) ([]propagationLevel, error) {
	st.mu.lock()
	defer st.mu.unlock()
	work := st.state.clone()
	levels, err := st.removeLink(work, nil, link, from, to)
	if err != nil {
		return nil, err
	}
	work.commitInto(st.state)
	st.log("remove_link", map[string]any{
		"link": link, "from": from, "to": to, "propagation": levels,
	})
	return levels, nil
}

type OpKind int

const (
	OpSetAttribute OpKind = iota + 1
	OpAddLink
	OpRemoveLink
)

type Op struct {
	Kind OpKind
	ID   ObjectID
	Prop PropertyName
	Val  PropertyValue
	Link LinkTypeName
	To   ObjectID
}

// SetIndexFailHook 注入故障：每次派生索引更新被应用前调用；
// 返回非 nil 时整个变更单元回滚，用于验证原子性。
func (st *Store) SetIndexFailHook(h func() error) {
	st.mu.lock()
	defer st.mu.unlock()
	st.failHook = h
}

func (st *Store) log(op string, detail map[string]any) {
	st.journal.append(op, detail)
}

func describeOps(ops []Op) []string {
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		switch op.Kind {
		case OpSetAttribute:
			out = append(out, "set("+string(op.ID)+","+string(op.Prop)+")")
		case OpAddLink:
			out = append(out, "add("+string(op.Link)+","+string(op.ID)+"->"+string(op.To)+")")
		case OpRemoveLink:
			out = append(out, "remove("+string(op.Link)+","+string(op.ID)+"->"+string(op.To)+")")
		}
	}
	return out
}
