package ontology

// Query 读取某起点实例的聚合结果；查询不修改任何聚合状态（只读锁）。
func (g *Graph) Query(view, start string) (Aggregate, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	vs, ok := g.byName[view]
	if !ok {
		return Aggregate{}, errf(KindTypeMismatch, "view %q not registered", view)
	}
	if _, exists := g.objects[start]; !exists {
		return Aggregate{}, errf(KindInstanceNotFound, "start object %q", start)
	}
	info, ok := vs.snap.agg[start]
	if !ok || !info.present {
		return Aggregate{Present: false}, nil
	}
	return Aggregate{Present: true, Value: info.value}, nil
}

// RescanCost 返回自上次清零以来重定最大值来源时考察过的终点实例总数。
func (g *Graph) RescanCost(view string) int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	vs, ok := g.byName[view]
	if !ok {
		return 0
	}
	return vs.snap.rescanCost
}

// ResetRescanCost 清零重定源开销计数。
func (g *Graph) ResetRescanCost(view string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if vs, ok := g.byName[view]; ok {
		vs.snap.rescanCost = 0
	}
}

// AddTypeToHop 为某一跳允许的类型集合新增成员（结构性变更）。
// 类型放宽后基于当前图重算；既有连通关系只会被保留或扩展，不会失效。
func (g *Graph) AddTypeToHop(view string, hop int, typ string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	vs, ok := g.byName[view]
	if !ok {
		return errf(KindTypeMismatch, "view %q not registered", view)
	}
	if _, exists := g.types[typ]; !exists {
		return errf(KindTypeMismatch, "type %q not registered", typ)
	}
	n := len(vs.spec.Path.Links)
	if hop < 0 || hop > n {
		return errf(KindTypeMismatch, "view %q: hop position %d out of range [0,%d]", view, hop, n)
	}
	// 与相邻链接签名匹配性检查（类型不匹配是最高优先级）。
	if hop < n {
		if !linkAllowsSrc(g.linkTypes[vs.spec.Path.Links[hop]], typ) {
			return errf(KindTypeMismatch,
				"view %q: new source type %q at position %d has no declared pair on hop link",
				view, typ, hop)
		}
	}
	if hop > 0 {
		if !linkAllowsDst(g.linkTypes[vs.spec.Path.Links[hop-1]], typ) {
			return errf(KindTypeMismatch,
				"view %q: new target type %q at position %d has no declared pair on previous hop link",
				view, typ, hop)
		}
	}
	if contains(vs.spec.Path.Types[hop], typ) {
		return nil // 已是成员，幂等
	}
	vs.spec.Path.Types[hop] = append(vs.spec.Path.Types[hop], typ)
	vs.cycleExcludable = staticallyCycleFree(vs.spec.Path)
	ns := g.buildSnapshot(vs)
	affected := diffAffected(vs.snap.reach, ns.reach)
	vs.snap = ns
	g.logChange(sprintf("AddTypeToHop view=%s pos=%d type=%s", view, hop, typ), affected, nil)
	return nil
}

func linkAllowsSrc(sigs []linkSig, typ string) bool {
	for _, s := range sigs {
		if s.srcType == typ {
			return true
		}
	}
	return false
}

func linkAllowsDst(sigs []linkSig, typ string) bool {
	for _, s := range sigs {
		if s.dstType == typ {
			return true
		}
	}
	return false
}

// injectFailure 安排下一次链接变更在视图维护提交前失败（测试钩子）。
func (g *Graph) injectFailure(view string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if vs, ok := g.byName[view]; ok {
		vs.failNext = true
	}
}
