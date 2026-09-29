package ontology

import "reflect"

// SetBase 给基视图写入外部值，并把该视图连同全部下游传递闭包标记为脏。
// 该方法不触发求值；需要原子地“设值并重算”时用 SetBaseAndRecompute。
func (g *Graph) SetBase(name string, value any) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	vs, ok := g.views[name]
	if !ok {
		return graphError(ErrNameNotRegistered, name, "cannot set an unregistered view")
	}
	if len(vs.spec.Dependencies) != 0 {
		return graphError(ErrNotBaseView, name, "only base views (no dependencies) accept external values")
	}

	vs.value = value
	vs.materialized = true
	g.markDownstreamDirty(name)
	return nil
}

// SetBaseAndRecompute 原子地完成设值与整轮重算：
// 持锁期间任何并发读者只能看到重算前或重算后的完整状态。
func (g *Graph) SetBaseAndRecompute(name string, value any) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	vs, ok := g.views[name]
	if !ok {
		return graphError(ErrNameNotRegistered, name, "cannot set an unregistered view")
	}
	if len(vs.spec.Dependencies) != 0 {
		return graphError(ErrNotBaseView, name, "only base views (no dependencies) accept external values")
	}

	// 先在影子状态中设值并传播脏标记，重算失败则丢弃影子状态，原状态不变。
	shadow := g.shadowSet(name, value)
	if err := g.recomputeShadow(shadow); err != nil {
		return err
	}
	g.commitShadow(shadow)
	g.generation++
	return nil
}

// shadow 保存一次“试写”的值与脏标记，失败时直接丢弃即可。
type shadow struct {
	values map[string]any
	mats   map[string]bool
	dirty  map[string]bool
}

func (g *Graph) shadowSet(name string, value any) *shadow {
	sh := &shadow{
		values: map[string]any{name: value},
		mats:   map[string]bool{name: true},
		dirty:  map[string]bool{},
	}
	queue := []string{name}
	seen := map[string]bool{name: true}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for down := range g.dependents[cur] {
			if !seen[down] {
				seen[down] = true
				sh.dirty[down] = true
				queue = append(queue, down)
			}
		}
	}
	return sh
}

func (g *Graph) valIn(sh *shadow, name string) (any, bool) {
	if v, ok := sh.values[name]; ok {
		return v, true
	}
	vs := g.views[name]
	return vs.value, vs.materialized
}

func (g *Graph) dirtyIn(sh *shadow, name string) bool {
	if d, ok := sh.dirty[name]; ok {
		return d
	}
	return g.views[name].dirty
}

// recomputeShadow 在影子状态上按拓扑序重算所有脏视图。
func (g *Graph) recomputeShadow(sh *shadow) error {
	dirty := make([]string, 0)
	for _, name := range g.order {
		if g.dirtyIn(sh, name) {
			dirty = append(dirty, name)
		}
	}
	dirtySet := make(map[string]bool, len(dirty))
	for _, name := range dirty {
		dirtySet[name] = true
	}

	indeg := make(map[string]int, len(dirty))
	downstream := make(map[string][]string, len(dirty))
	for _, name := range dirty {
		for _, dep := range g.views[name].spec.Dependencies {
			if dirtySet[dep] {
				indeg[name]++
				downstream[dep] = append(downstream[dep], name)
			}
		}
	}

	ready := make([]string, 0, len(dirty))
	for _, name := range dirty {
		if indeg[name] == 0 {
			ready = append(ready, name)
		}
	}
	seq := make([]string, 0, len(dirty))
	for len(ready) > 0 {
		name := ready[0]
		ready = ready[1:]
		seq = append(seq, name)
		for _, down := range downstream[name] {
			indeg[down]--
			if indeg[down] == 0 {
				ready = insertByIndex(ready, down, g.views)
			}
		}
	}
	if len(seq) != len(dirty) {
		return graphError(ErrCycleDetected, "", "internal error: dirty subgraph contains a cycle")
	}

	for _, name := range seq {
		vs := g.views[name]
		if len(vs.spec.Dependencies) == 0 {
			// 脏的基视图只可能是本次被设值的根（值已在影子表中），
			// 或此前从未设值——后者直接拒绝。
			if _, ok := sh.values[name]; ok {
				continue
			}
			if !vs.materialized {
				return graphError(ErrBaseNotSet, name, "base view has never been assigned an external value")
			}
			continue
		}
		deps := make([]any, len(vs.spec.Dependencies))
		for i, dep := range vs.spec.Dependencies {
			if _, ok := g.views[dep]; !ok {
				return graphError(ErrUnknownDep, dep, "dependency referenced by view '"+name+"' is not registered")
			}
			val, mat := g.valIn(sh, dep)
			if !mat {
				if len(g.views[dep].spec.Dependencies) == 0 {
					return graphError(ErrBaseNotSet, dep, "base dependency of view '"+name+"' has not been set")
				}
				return graphError(ErrNotMaterialized, dep, "dependency of view '"+name+"' has not been materialized yet")
			}
			deps[i] = val
		}
		val, err := vs.spec.Fn(deps)
		if err != nil {
			return graphError(ErrComputeFailed, name, err.Error())
		}
		sh.values[name] = val
		sh.mats[name] = true
	}
	return nil
}

func (g *Graph) commitShadow(sh *shadow) {
	for name, val := range sh.values {
		g.views[name].value = val
	}
	for name := range sh.mats {
		g.views[name].materialized = true
	}
	for name := range sh.dirty {
		g.views[name].dirty = false
	}
}

// Get 读取单个视图在最近一次完整重算之后的值。
func (g *Graph) Get(name string) (any, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	vs, ok := g.views[name]
	if !ok {
		return nil, graphError(ErrNameNotRegistered, name, "cannot read an unregistered view")
	}
	if !vs.materialized {
		return nil, graphError(ErrNotMaterialized, name, "view has not been materialized yet; run Recompute first")
	}
	return vs.value, nil
}

// Snapshot 返回某次完整重算之后全部已物化视图值的一致副本。
// 副本可在锁外自由使用，与后续变更相互隔离。
func (g *Graph) Snapshot() (map[string]any, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	snap := make(map[string]any, len(g.views))
	for _, name := range g.order {
		vs := g.views[name]
		if vs.materialized {
			snap[name] = vs.value
		}
	}
	return snap, nil
}

// Generation 返回当前已完成的重算轮次号，每次成功重算后单调递增，
// 可供并发读者判断自己读到的是否为同一轮完整状态。
func (g *Graph) Generation() uint64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.generation
}

// Verify 用独立的全量重算实现从基视图出发推导所有视图，
// 并与当前物化结果逐一对照；任何不一致都返回错误。
// 校验期间持读锁，因此对照的是某一轮完整重算后的稳定状态。
func (g *Graph) Verify() error {
	g.mu.RLock()
	defer g.mu.RUnlock()

	expected := make(map[string]any, len(g.views))
	for _, name := range g.order {
		vs := g.views[name]
		if len(vs.spec.Dependencies) == 0 {
			if !vs.materialized {
				continue // 尚未设值的基视图及其下游不参与对照
			}
			expected[name] = vs.value
			continue
		}

		deps := make([]any, len(vs.spec.Dependencies))
		ready := true
		for i, dep := range vs.spec.Dependencies {
			if _, ok := g.views[dep]; !ok {
				return graphError(ErrUnknownDep, dep, "dependency referenced by view '"+name+"' is not registered")
			}
			val, ok := expected[dep]
			if !ok {
				ready = false
				break
			}
			deps[i] = val
		}
		if !ready {
			continue
		}
		val, err := vs.spec.Fn(deps)
		if err != nil {
			return graphError(ErrComputeFailed, name, err.Error())
		}
		expected[name] = val
	}

	for name, want := range expected {
		vs := g.views[name]
		if !vs.materialized {
			return graphError(ErrNotMaterialized, name, "full recomputation produced a value but view is not materialized")
		}
		if !reflect.DeepEqual(vs.value, want) {
			return graphError(ErrComputeFailed, name, "materialized value does not match full recomputation")
		}
	}
	return nil
}
