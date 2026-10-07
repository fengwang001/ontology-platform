package aggview

// initViewLocked 在视图刚声明（或需全量对齐）时，对全部起点类型实例
// 依据当前图重算可达终点集合与最大值。调用方必须持有 store 写锁。
func (e *Engine) initViewLocked(v *view) {
	e.rebuildFromSources(v, e.sourceIDsLocked(v))
}

// recomputeAllLocked 在结构性变更（新增跳类型成员）后全量重建视图，
// 返回受影响（结果发生变化）的起点集合用于日志。
// 既有连通关系不会失效：重建只可能保持或扩展可达集合。
func (e *Engine) recomputeAllLocked(v *view) []Affected {
	var changed []string
	for _, s := range e.sourceIDsLocked(v) {
		old := v.state.get(s)
		newSet := reachable(e, v.path, s)
		values := e.terminalValuesLocked(v.path, newSet)
		v.state.rebuild(s, newSet, values)
		if !equalResult(old, v.state.get(s)) {
			changed = append(changed, s)
		}
	}
	return []Affected{{Sources: changed,
		Reason: "structural hop-type expansion; full reachability realignment"}}
}

func (e *Engine) rebuildFromSources(v *view, sources []string) {
	for _, s := range sources {
		set := reachable(e, v.path, s)
		values := e.terminalValuesLocked(v.path, set)
		v.state.rebuild(s, set, values)
	}
}

func (e *Engine) sourceIDsLocked(v *view) []string {
	var ids []string
	for _, o := range e.store.AllObjectsLocked() {
		if o.Type == v.path.source {
			ids = append(ids, o.ID)
		}
	}
	return ids
}

func (e *Engine) terminalValuesLocked(p *viewPath, set map[string]struct{}) map[string]float64 {
	values := make(map[string]float64, len(set))
	for t := range set {
		if v, ok := e.store.AttrLocked(t, p.attr); ok {
			values[t] = v
		}
	}
	return values
}

func equalResult(a, b Result) bool {
	if a.Absent != b.Absent {
		return false
	}
	if a.Absent {
		return true
	}
	return a.Max == b.Max
}
