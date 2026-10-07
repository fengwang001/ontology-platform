package aggview

import (
	"fmt"
	"sort"

	"ontology/ontology"
)

// CostStats 统计"最大值重新确定"过程中实际考察的终点实例数量，
// 用于证明开销不超过受影响起点当前真实可达终点总数。
type CostStats struct {
	// Reexamined：属性变小且旧终点为最大值来源时，逐起点重新扫描所考察
	// 的终点实例数量之和。
	Reexamined int
	// Reachable：这些起点当前真实可达终点总数（理论上界）。
	Reachable int
}

// AddLink 新增一条链接并在同一临界区内维护全部受影响视图。
func (e *Engine) AddLink(l ontology.Link) (CostStats, *ontology.AggregateError) {
	e.store.Lock()
	defer e.store.Unlock()

	// 错误优先级 2：实例存在性。
	if !e.store.HasObjectLocked(l.From) {
		return CostStats{}, ontology.NewInstanceNotFoundError(fmt.Sprintf("from instance %q not found", l.From))
	}
	if !e.store.HasObjectLocked(l.To) {
		return CostStats{}, ontology.NewInstanceNotFoundError(fmt.Sprintf("to instance %q not found", l.To))
	}

	// 链接仅对"关系被路径使用且两端类型匹配该跳声明"的视图生效；
	// 对其余视图属于类型不匹配（不影响该视图，静默忽略）。
	applicable := e.applicableViewsLocked(l)
	// 若存在视图把该关系用于某一跳，但本链接两端类型与该跳声明不匹配，
	// 则属于"路径声明中对象类型不匹配"（优先级 1）。
	if me := e.linkTypeMismatchLocked(l); me != nil {
		return CostStats{}, me
	}

	// 错误优先级 3：运行时环检查（仅对声明阶段无法静态排除环的视图）。
	for _, vi := range applicable {
		if e.cycleIntroduced(vi.v.path, vi.hop, l.From, l.To) {
			return CostStats{}, ontology.NewCycleDetectedError(
				fmt.Sprintf("view %q: link %s->%s at hop %d introduces a cycle not statically excludable",
					vi.v.path.name, l.From, l.To, vi.hop))
		}
	}

	// 影子备份受影响视图，维护失败时连同存储改动整体回滚（优先级 4）。
	shadows := e.snapshotViewsLocked(applicable)
	cp := l
	if !e.store.AddLinkLocked(&cp) {
		e.restoreViewsLocked(shadows)
		// 重复 ID 属于结构性冲突，归类为类型/声明不匹配。
		return CostStats{}, ontology.NewTypeMismatchError(fmt.Sprintf("link %q already exists", l.ID))
	}

	var allAffected []Affected
	for _, vi := range applicable {
		if vi.v.fault {
			vi.v.fault = false
			e.store.RemoveLinkLocked(cp.ID)
			e.restoreViewsLocked(shadows)
			return CostStats{}, ontology.NewMaintenanceFailedError(
				"injected maintenance failure during add link; store and views rolled back")
		}
		aff, merr := e.maintainAddLinkLocked(vi.v, vi.hop, &cp)
		if merr != nil {
			e.store.RemoveLinkLocked(cp.ID)
			e.restoreViewsLocked(shadows)
			return CostStats{}, merr
		}
		allAffected = append(allAffected, aff)
	}
	e.appendLog("add_link",
		fmt.Sprintf("link=%s %s-%s->%s", l.ID, l.From, l.Rel, l.To), allAffected)
	return CostStats{}, nil
}

// RemoveLink 删除一条链接并维护受影响视图。
func (e *Engine) RemoveLink(linkID string) (CostStats, *ontology.AggregateError) {
	e.store.Lock()
	defer e.store.Unlock()

	l, ok := e.store.GetLinkLocked(linkID)
	if !ok {
		return CostStats{}, ontology.NewInstanceNotFoundError(fmt.Sprintf("link %q not found", linkID))
	}
	applicable := e.applicableViewsLocked(l)
	shadows := e.snapshotViewsLocked(applicable)

	// 在删除前的图上精确圈定候选起点：s 存在一条长度为 i 的走法到达
	// l.From（前缀），即存在经过本链接的长度 k 走法。这是教科书式的
	// prefix/suffix 受影响对公式的前缀半边；必须在删除链接之前计算。
	candidatesByView := make([]map[string]struct{}, len(applicable))
	for idx, vi := range applicable {
		preds := sourcesReaching(e, vi.v.path, vi.hop, l.From, e.levelTypeOK(vi.v.path))
		candidatesByView[idx] = filterSourceTypeLocked(e, vi.v.path, preds)
	}

	removed := e.store.RemoveLinkLocked(linkID)
	if removed == nil {
		e.restoreViewsLocked(shadows)
		return CostStats{}, ontology.NewMaintenanceFailedError("link vanished during removal")
	}

	var allAffected []Affected
	for idx, vi := range applicable {
		if vi.v.fault {
			vi.v.fault = false
			e.store.AddLinkLocked(removed)
			e.restoreViewsLocked(shadows)
			return CostStats{}, ontology.NewMaintenanceFailedError(
				"injected maintenance failure during remove link; store and views rolled back")
		}
		aff, merr := e.maintainRemoveLinkLocked(vi.v, vi.hop, removed, candidatesByView[idx])
		if merr != nil {
			e.store.AddLinkLocked(removed)
			e.restoreViewsLocked(shadows)
			return CostStats{}, merr
		}
		allAffected = append(allAffected, aff)
	}
	e.appendLog("remove_link",
		fmt.Sprintf("link=%s %s-%s->%s", l.ID, l.From, l.Rel, l.To), allAffected)
	return CostStats{}, nil
}

// WriteAttr 写入实例的数值属性并维护相关视图。
func (e *Engine) WriteAttr(objectID, attr string, value float64) (CostStats, *ontology.AggregateError) {
	e.store.Lock()
	defer e.store.Unlock()

	if !e.store.HasObjectLocked(objectID) {
		return CostStats{}, ontology.NewInstanceNotFoundError(fmt.Sprintf("instance %q not found", objectID))
	}
	old, had := e.store.WriteAttrLocked(objectID, attr, value)

	var stats CostStats
	var allAffected []Affected
	for _, v := range e.views {
		if v.path.attr != attr || v.path.target != e.typeOf(objectID) {
			continue
		}
		aff, sc := e.maintainAttrLocked(v, objectID, old, had, value)
		stats.Reexamined += sc.Reexamined
		stats.Reachable += sc.Reachable
		allAffected = append(allAffected, aff)
	}
	e.appendLog("write_attr",
		fmt.Sprintf("object=%s attr=%s old=%v(new=%v)", objectID, attr, old, value), allAffected)
	return stats, nil
}

type viewHop struct {
	v   *view
	hop int
}

func (e *Engine) applicableViewsLocked(l ontology.Link) []viewHop {
	ft := e.typeOf(l.From)
	tt := e.typeOf(l.To)
	var res []viewHop
	for _, v := range e.views {
		for i, h := range v.path.hops {
			if h.relation != l.Rel {
				continue
			}
			if !v.path.allowsType(i, tt) {
				continue
			}
			if i == 0 {
				if ft != v.path.source {
					continue
				}
			} else if !v.path.allowsType(i-1, ft) {
				continue
			}
			res = append(res, viewHop{v: v, hop: i})
			break
		}
	}
	return res
}

// linkTypeMismatchLocked 检查：是否有任何视图在某一跳声明了 l.Rel，
// 但链接终点类型不在该跳允许集合，或起点端类型不符合前一层类型。
func (e *Engine) linkTypeMismatchLocked(l ontology.Link) *ontology.AggregateError {
	ft := e.typeOf(l.From)
	tt := e.typeOf(l.To)
	for _, v := range e.views {
		for i, h := range v.path.hops {
			if h.relation != l.Rel {
				continue
			}
			if !v.path.allowsType(i, tt) {
				return ontology.NewTypeMismatchError(fmt.Sprintf(
					"view=%q hop=%d: link target type %q not allowed", v.path.name, i, tt))
			}
			if i == 0 {
				if ft != v.path.source {
					return ontology.NewTypeMismatchError(fmt.Sprintf(
						"view=%q hop=0: link source type %q != source type %q",
						v.path.name, ft, v.path.source))
				}
			} else if !v.path.allowsType(i-1, ft) {
				return ontology.NewTypeMismatchError(fmt.Sprintf(
					"view=%q hop=%d: link source type %q not allowed at preceding level",
					v.path.name, i, ft))
			}
		}
	}
	return nil
}

func (e *Engine) maintainAddLinkLocked(v *view, i int, l *ontology.Link) (Affected, *ontology.AggregateError) {
	// 候选起点 = 能沿前 i 跳到达 l.From 的起点实例；由反向 BFS 精确圈定。
	preds := sourcesReaching(e, v.path, i, l.From, e.levelTypeOK(v.path))
	candidates := filterSourceTypeLocked(e, v.path, preds)

	var affected []string
	for s := range candidates {
		newSet := reachable(e, v.path, s)
		// 新链接可能只是提供了一条冗余走法而不改变可达终点集合；
		// 集合不变则不更新、不计入受影响集合，避免多算。
		if sameSet(v.state.reachableSet(s), newSet) {
			continue
		}
		v.state.rebuild(s, newSet, e.terminalValuesLocked(v.path, newSet))
		affected = append(affected, s)
	}
	sort.Strings(affected)
	return Affected{Sources: affected, Reason: fmt.Sprintf(
		"view=%s hop=%d add %s->%s candidates=%d (forward-prefix reachability + set diff)",
		v.path.name, i, l.From, l.To, len(candidates))}, nil
}

func (e *Engine) maintainRemoveLinkLocked(v *view, i int, l *ontology.Link,
	candidates map[string]struct{}) (Affected, *ontology.AggregateError) {
	// candidates 已在删除前的图上由"i 跳前缀可达 l.From"精确求得。
	var affected []string
	for s := range candidates {
		newSet := reachable(e, v.path, s)
		if sameSet(v.state.reachableSet(s), newSet) {
			continue
		}
		v.state.rebuild(s, newSet, e.terminalValuesLocked(v.path, newSet))
		affected = append(affected, s)
	}
	sort.Strings(affected)
	return Affected{Sources: affected, Reason: fmt.Sprintf(
		"view=%s hop=%d remove %s->%s candidates=%d (recompute + set diff)",
		v.path.name, i, l.From, l.To, len(candidates))}, nil
}

func (e *Engine) maintainAttrLocked(v *view, t string, old float64, had bool, newVal float64) (Affected, CostStats) {
	var affected []string
	var stats CostStats
	for s := range v.state.reachable {
		// 只处理当前确实能够到达 t 的起点。
		if !v.state.contains(s, t) {
			continue
		}
		affected = append(affected, s)
		before := v.state.get(s)
		if val, ok := e.store.AttrLocked(t, v.path.attr); ok {
			v.state.setTerm(s, t, val)
		} else {
			v.state.deleteTerm(s, t)
		}
		// 旧值恰为该起点最大值来源且属性变小：重新确定来源。
		// maxOf 只扫描该起点现存贡献者（<= 其当前真实可达终点数），
		// 绝不扫描任何与该起点无关的其他实例。
		if had && newVal < old && !before.Absent && before.Max == old {
			_, n := v.state.maxOf(s)
			stats.Reexamined += n
			stats.Reachable += v.state.reachableCount(s)
		}
	}
	sort.Strings(affected)
	return Affected{Sources: affected, Reason: fmt.Sprintf(
		"view=%s terminal=%s %v->%v (only sources currently reaching it)",
		v.path.name, t, old, newVal)}, stats
}

func filterSourceTypeLocked(e *Engine, p *viewPath, ids map[string]struct{}) map[string]struct{} {
	out := map[string]struct{}{}
	for id := range ids {
		if t, ok := e.store.ObjectTypeOfLocked(id); ok && t == p.source {
			out[id] = struct{}{}
		}
	}
	return out
}

func sameSet(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for x := range b {
		if _, ok := a[x]; !ok {
			return false
		}
	}
	return true
}

type shadow struct {
	v     *view
	reach map[string]map[string]struct{}
	terms map[string]map[string]float64
	open  bool
}

func (e *Engine) snapshotViewsLocked(list []viewHop) []shadow {
	seen := map[*view]bool{}
	var sh []shadow
	for _, vi := range list {
		if seen[vi.v] {
			continue
		}
		seen[vi.v] = true
		reach := make(map[string]map[string]struct{}, len(vi.v.state.reachable))
		for s, set := range vi.v.state.reachable {
			cp := make(map[string]struct{}, len(set))
			for t := range set {
				cp[t] = struct{}{}
			}
			reach[s] = cp
		}
		terms := make(map[string]map[string]float64, len(vi.v.state.terms))
		for s, ms := range vi.v.state.terms {
			cp := make(map[string]float64, len(ms))
			for t, val := range ms {
				cp[t] = val
			}
			terms[s] = cp
		}
		sh = append(sh, shadow{v: vi.v, reach: reach, terms: terms, open: vi.v.open})
	}
	return sh
}

func (e *Engine) restoreViewsLocked(sh []shadow) {
	for _, s := range sh {
		s.v.state.reachable = s.reach
		s.v.state.terms = s.terms
		s.v.open = s.open
	}
}
