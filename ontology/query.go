package ontology

import "sort"

// QueryResult 是一次派生索引查询命中的单个下游实例。
type QueryResult struct {
	ID    ObjectID
	Value PropertyValue
}

// QueryIndex 返回类型 t 上派生属性 prop 当前键值等于 value 的全部下游实例。
// 仅 StateIndexable 状态的实例会成为查询结果；不可索引实例永不出现。
// 读取全程持有读锁，与任一写事务互斥，因此查询等价于发生在某个与变更
// 次序一致的串行时间点上（严格可串行化）。
func (st *Store) QueryIndex(t ObjectTypeName, prop PropertyName, value string) ([]QueryResult, error) {
	st.mu.rLock()
	defer st.mu.rUnlock()
	var errs []error
	if !st.schema.hasProperty(t, prop) {
		errs = append(errs, newError(KindSourceNotFound,
			"type %q has no property %q", t, prop))
	}
	if _, ok := st.schema.derivedIndex(t, prop); !ok {
		errs = append(errs, newError(KindLinkTypeNotSupported,
			"(%q,%q) is not a derived index", t, prop))
	}
	if err := classify(errs); err != nil {
		return nil, err
	}
	members := st.state.index[propKey{t, prop}][value]
	ids := make([]ObjectID, 0, len(members))
	for id := range members {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]QueryResult, 0, len(ids))
	for _, id := range ids {
		out = append(out, QueryResult{ID: id, Value: PropertyValue{Val: value, Has: true}})
	}
	return out, nil
}

// Resolve 返回某个实例在某个派生属性上的当前索引状态（含明确的不可索引状态）。
func (st *Store) Resolve(id ObjectID, prop PropertyName) (IndexState, error) {
	st.mu.rLock()
	defer st.mu.rUnlock()
	ins, ok := st.state.inst[id]
	if !ok {
		return IndexState{}, newError(KindSourceNotFound, "instance %q not found", id)
	}
	d, ok := st.schema.derivedIndex(ins.typ, prop)
	if !ok {
		if !st.schema.hasProperty(ins.typ, prop) {
			return IndexState{}, newError(KindSourceNotFound,
				"type %q has no property %q", ins.typ, prop)
		}
		return IndexState{}, newError(KindLinkTypeNotSupported,
			"(%q,%q) is not a derived index", ins.typ, prop)
	}
	stt, ok := st.state.states[id][prop]
	if !ok {
		// 兜底：派生状态通常在实例创建时物化；缺失时即时求值。
		stt = evaluate(st.state, st.schema, id, d)
	}
	return stt, nil
}

// NativeAttribute 读取一个普通（非派生）属性的当前值，供测试与对拍模型使用。
func (st *Store) NativeAttribute(id ObjectID, prop PropertyName) (PropertyValue, bool, error) {
	st.mu.rLock()
	defer st.mu.rUnlock()
	ins, ok := st.state.inst[id]
	if !ok {
		return PropertyValue{}, false, newError(KindSourceNotFound, "instance %q not found", id)
	}
	v, has := ins.props[prop]
	return v, has, nil
}
