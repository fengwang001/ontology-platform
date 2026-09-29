package ontology

import "sort"

// Recompute 对当前全部脏视图做一轮重算：
// 依赖先于被依赖者求值；同一轮内每个脏视图至多求值一次；
// 未标记为脏的视图不会被求值。
// 任何校验或计算函数失败时，本轮已产生的临时值全部丢弃，图状态不变。
func (g *Graph) Recompute() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.recomputeLocked()
}

// recomputeLocked 在调用方持有写锁时执行一轮重算。
func (g *Graph) recomputeLocked() error {
	dirty := make([]string, 0)
	for _, name := range g.order {
		if g.views[name].dirty {
			dirty = append(dirty, name)
		}
	}
	if len(dirty) == 0 {
		return nil
	}
	dirtySet := make(map[string]bool, len(dirty))
	for _, name := range dirty {
		dirtySet[name] = true
	}

	// 在脏子图上做 Kahn 拓扑排序，并列时按注册顺序打破平局，保证可复现。
	indeg := make(map[string]int, len(dirty))
	downstream := make(map[string][]string, len(dirty))
	for _, name := range dirty {
		vs := g.views[name]
		for _, dep := range vs.spec.Dependencies {
			if dirtySet[dep] {
				indeg[name]++
				downstream[dep] = append(downstream[dep], name)
			}
		}
	}
	for name := range downstream {
		sort.Slice(downstream[name], func(i, j int) bool {
			return g.views[downstream[name][i]].index < g.views[downstream[name][j]].index
		})
	}

	ready := make([]string, 0, len(dirty))
	for _, name := range dirty { // dirty 已按注册顺序排列
		if indeg[name] == 0 {
			ready = append(ready, name)
		}
	}

	seq := make([]string, 0, len(dirty))
	for len(ready) > 0 {
		name := ready[0]
		ready = ready[1:]
		seq = append(seq, name)
		next := downstream[name]
		// 保持 ready 始终按注册顺序
		for _, down := range next {
			indeg[down]--
			if indeg[down] == 0 {
				ready = insertByIndex(ready, down, g.views)
			}
		}
	}
	if len(seq) != len(dirty) {
		// 注册时已保证无环；走到这里说明内部不变量被破坏。
		return graphError(ErrCycleDetected, "", "internal error: dirty subgraph contains a cycle")
	}

	// 在临时表中完成全部求值，全部成功后才一次性提交，保证失败无副作用。
	values := make(map[string]any, len(seq))
	for _, name := range seq {
		vs := g.views[name]
		if len(vs.spec.Dependencies) == 0 {
			if !vs.materialized {
				return graphError(ErrBaseNotSet, name, "base view has never been assigned an external value")
			}
			values[name] = vs.value
			continue
		}

		deps := make([]any, len(vs.spec.Dependencies))
		for i, dep := range vs.spec.Dependencies {
			depView, ok := g.views[dep]
			if !ok {
				return graphError(ErrUnknownDep, dep, "dependency referenced by view '"+name+"' is not registered")
			}
			if val, fresh := values[dep]; fresh {
				deps[i] = val
			} else {
				if !depView.materialized {
					if len(depView.spec.Dependencies) == 0 {
						return graphError(ErrBaseNotSet, dep, "base dependency of view '"+name+"' has not been set")
					}
					return graphError(ErrNotMaterialized, dep, "dependency of view '"+name+"' has not been materialized yet")
				}
				deps[i] = depView.value
			}
		}

		val, err := vs.spec.Fn(deps)
		if err != nil {
			return graphError(ErrComputeFailed, name, err.Error())
		}
		values[name] = val
	}

	for _, name := range seq {
		vs := g.views[name]
		vs.value = values[name]
		vs.materialized = true
		vs.dirty = false
	}
	g.generation++
	return nil
}

// insertByIndex 按注册序号把 name 插入保持有序的 ready 列表。
func insertByIndex(ready []string, name string, views map[string]*viewState) []string {
	pos := len(ready)
	nameIndex := views[name].index
	for i, cur := range ready {
		if nameIndex < views[cur].index {
			pos = i
			break
		}
	}
	ready = append(ready, "")
	copy(ready[pos+1:], ready[pos:])
	ready[pos] = name
	return ready
}
