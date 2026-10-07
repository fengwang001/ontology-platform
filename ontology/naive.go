package ontology

// NaiveRebuild 是独立实现的朴素逐事件重放模型：不使用任何
// 检查点，从首个事件开始逐个重放，用于与 Rebuilder 对照。
func NaiveRebuild(schema *SchemaRegistry, events []Event, cutoff int64) (State, error) {
	eligible, err := canonical(events, cutoff)
	if err != nil {
		return State{}, err
	}
	st := State{Values: map[string]Value{}, Suppressed: map[string]Value{}}
	current := ""
	for _, ev := range eligible {
		v := schema.VersionAt(ev.Time)
		switch ev.Kind {
		case EventCreate:
			if _, ok := v.Types[ev.TypeID]; !ok {
				return State{}, ErrUndefinedTargetType
			}
			if current == "" {
				current = ev.TypeID
			}
		case EventSetProperty:
			if current == "" {
				continue
			}
			rng, ok := Resolve(v, current, ev.Property)
			if ok && rng.Contains(ev.Value) {
				st.Values[ev.Property] = ev.Value
			}
		case EventEvolveType:
			if _, ok := v.Types[ev.TypeID]; !ok {
				return State{}, ErrUndefinedTargetType
			}
			if current == "" {
				return State{}, ErrInvalidEvolutionPath
			}
			parent := v.Types[current].Parent
			if ev.TypeID != parent && !IsDirectChild(v, current, ev.TypeID) {
				return State{}, ErrInvalidEvolutionPath
			}
			current = ev.TypeID
		}
	}
	// 截止时刻遮蔽：按 cutoff 生效的规则版本与最终类型，
	// 把不再有意义或取值越界的历史赋值移入 Suppressed。
	vc := schema.VersionAt(cutoff)
	for prop, val := range st.Values {
		rng, ok := Resolve(vc, current, prop)
		if !ok || !rng.Contains(val) {
			st.Suppressed[prop] = val
			delete(st.Values, prop)
		}
	}
	st.TypeID = current
	return st, nil
}
