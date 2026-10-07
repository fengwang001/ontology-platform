package ontology

// validateSpec 校验路径声明：长度、链接声明、逐跳类型签名匹配。
// 这是最高优先级错误（KindTypeMismatch）。
func (g *Graph) validateSpec(spec ViewSpec) error {
	p := spec.Path
	if len(p.Links) == 0 {
		return errf(KindTypeMismatch, "view %q: path must contain at least one hop", spec.Name)
	}
	if len(p.Types) != len(p.Links)+1 {
		return errf(KindTypeMismatch, "view %q: %d hops require %d type sets, got %d",
			spec.Name, len(p.Links), len(p.Links)+1, len(p.Types))
	}
	for i, ts := range p.Types {
		if len(ts) == 0 {
			return errf(KindTypeMismatch, "view %q: type set at position %d is empty", spec.Name, i)
		}
		for _, t := range ts {
			if _, ok := g.types[t]; !ok {
				return errf(KindTypeMismatch, "view %q: type %q at position %d not registered", spec.Name, t, i)
			}
		}
	}
	for i, lname := range p.Links {
		sigs, ok := g.linkTypes[lname]
		if !ok {
			return errf(KindTypeMismatch, "view %q: link %q at hop %d not declared", spec.Name, lname, i)
		}
		okPair := false
		for _, sig := range sigs {
			if contains(p.Types[i], sig.srcType) && contains(p.Types[i+1], sig.dstType) {
				okPair = true
				break
			}
		}
		if !okPair {
			return errf(KindTypeMismatch,
				"view %q: hop %d link %q has no declared type pair allowed by positions %d,%d",
				spec.Name, i, lname, i, i+1)
		}
	}
	return nil
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// staticallyCycleFree 判断路径各位置类型集合两两不相交，则环可在声明阶段静态排除。
func staticallyCycleFree(p Path) bool {
	seen := map[string]struct{}{}
	for _, ts := range p.Types {
		for _, t := range ts {
			if _, dup := seen[t]; dup {
				return false
			}
			seen[t] = struct{}{}
		}
	}
	return true
}

// buildSnapshot 基于当前图重算该视图的完整状态（分层 DP，天然有界、环安全）。
func (g *Graph) buildSnapshot(vs *viewState) *snapshot {
	n := len(vs.spec.Path.Links)
	s := &snapshot{
		reach:   map[reachKey]int64{},
		agg:     map[string]*aggInfo{},
		endRefs: map[string]map[string]struct{}{},
	}
	allow := g.allowAt(vs)
	// starts：第 0 层允许类型的全部对象。
	starts := map[string]int64{}
	for id, o := range g.objects {
		if _, okA := allow[0][o.typ]; okA {
			starts[id] = 1
		}
	}
	// 逐起点做分层 DP：层数即声明路径长度，环上的重复访问只会表现为计数累加，
	// 展开深度有界，因此不会无限递归。
	for start := range starts {
		layer := map[string]int64{start: 1}
		for k := 0; k < n; k++ {
			next := map[string]int64{}
			lname := vs.spec.Path.Links[k]
			for u, cu := range layer {
				for v, em := range g.out[lname][u] {
					if _, okA := allow[k+1][g.objects[v].typ]; !okA {
						continue
					}
					next[v] += cu * em
				}
			}
			layer = next
		}
		for end, c := range layer {
			if c > 0 {
				s.reach[reachKey{start: start, end: end}] = c
			}
		}
	}
	g.recomputeAggs(vs, s)
	return s
}

// allowAt 返回各位置允许的类型集合（含结构性新增）。
func (g *Graph) allowAt(vs *viewState) []map[string]struct{} {
	out := make([]map[string]struct{}, len(vs.spec.Path.Types))
	for i, ts := range vs.spec.Path.Types {
		m := make(map[string]struct{}, len(ts))
		for _, t := range ts {
			m[t] = struct{}{}
		}
		out[i] = m
	}
	return out
}

// recomputeAggs 依据 s.reach 与当前属性值重算全部起点聚合，并重建 endRefs。
func (g *Graph) recomputeAggs(vs *viewState, s *snapshot) {
	s.agg = map[string]*aggInfo{}
	s.endRefs = map[string]map[string]struct{}{}
	endsByStart := map[string][]string{}
	for k := range s.reach {
		endsByStart[k.start] = append(endsByStart[k.start], k.end)
		if s.endRefs[k.end] == nil {
			s.endRefs[k.end] = map[string]struct{}{}
		}
		s.endRefs[k.end][k.start] = struct{}{}
	}
	for start, ends := range endsByStart {
		info := &aggInfo{}
		for _, end := range ends {
			g.addEndToAgg(vs, s, info, end)
		}
		s.agg[start] = info
	}
}

// addEndToAgg 让单个终点对聚合贡献一次（重复到达天然只出现一次：reach 是集合键）。
func (g *Graph) addEndToAgg(vs *viewState, s *snapshot, info *aggInfo, end string) {
	val, ok := g.objects[end].attrs[vs.spec.Attr]
	if !ok {
		return
	}
	if !info.present || val > info.value {
		info.present = true
		info.value = val
		info.maxEnd = end
	}
}
