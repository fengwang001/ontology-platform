package lineage

import "sort"

func (t *Tracker) resolveLocked(ref Ref) (*Node, error) {
	vers, ok := t.nodes[ref.ID]
	if !ok {
		return nil, reject(ErrRefNotFound, "trace", &ref, "对象 ID 从未登记")
	}
	node := vers[ref.Version]
	if node == nil {
		return nil, reject(ErrRefNotFound, "trace", &ref, "该版本号在对象版本表中不存在")
	}
	cur, _ := t.currentVersionLocked(ref.ID)
	if node.Version != cur {
		return nil, reject(ErrStaleVersion, "trace", &ref,
			"查询起点版本 "+ref.Version+" 已过期，当前版本为 "+cur+"，血缘不得指向过期版本")
	}
	return node, nil
}

// Direct 返回目标当前版本的直接上游（Upstream）或直接下游（Downstream）引用，
// 并对涉及的血缘边做双向闭合与版本有效性校验。
func (t *Tracker) Direct(ref Ref, dir Direction) ([]Ref, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if _, err := t.resolveLocked(ref); err != nil {
		t.log.Error("血缘直接追溯被拒绝", "方向", dir, "原因", err.Error())
		return nil, err
	}
	node := t.nodes[ref.ID][ref.Version]

	var neighbors []string
	upstream := dir == Upstream
	if upstream {
		neighbors = mapKeys(t.in[ref.ID][ref.Version])
	} else {
		neighbors = mapKeys(t.out[ref.ID][ref.Version])
	}
	if err := t.crossCheckLocked(ref, node, upstream, neighbors); err != nil {
		t.log.Error("血缘直接追溯被拒绝", "方向", dir, "原因", err.Error())
		return nil, err
	}

	refs := make([]Ref, 0, len(neighbors))
	for _, k := range neighbors {
		other := parseRefKey(k)
		from, to := ref, other
		if dir == Upstream {
			from, to = other, ref
		}
		if err := t.checkEdgeLocked(from, to); err != nil {
			t.log.Error("血缘直接追溯被拒绝", "方向", dir, "原因", err.Error())
			return nil, err
		}
		refs = append(refs, other)
	}

	if upstream && node.Kind == Derived && len(refs) == 0 {
		err := reject(ErrMissingUpstream, "trace", &ref,
			"派生对象当前版本没有任何上游输入边")
		t.log.Error("血缘直接追溯被拒绝", "原因", err.Error())
		return nil, err
	}

	sort.Slice(refs, func(i, j int) bool { return refKey(refs[i]) < refKey(refs[j]) })
	t.log.Info("血缘直接追溯", "对象", ref.ID, "版本", ref.Version,
		"方向", dir, "结果数", len(refs),
		"判定依据", "仅取当前版本的直接血缘边，边端点必须仍为当前版本且双向索引一致")
	return refs, nil
}

// Trace 沿指定方向追溯完整血缘并返回规范化血缘图。
// Upstream：谁产生我；Downstream：我产生谁；Both：完整双向子图。
// 查询会校验：起点与所有可达端点版本有效、派生节点不缺上游、
// 每条输入边都有对应下游反向边。任一不满足即拒绝，且查询不修改任何状态。
func (t *Tracker) Trace(ref Ref, dir Direction) (*Graph, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	node, err := t.resolveLocked(ref)
	if err != nil {
		t.log.Error("血缘追溯被拒绝", "方向", dir, "原因", err.Error())
		return nil, err
	}

	visited := map[string]*Node{refKey(ref): node}
	if dir == Upstream || dir == Both {
		if err := t.walkLocked(ref, true, visited); err != nil {
			t.log.Error("血缘追溯被拒绝", "方向", dir, "原因", err.Error())
			return nil, err
		}
	}
	if dir == Downstream || dir == Both {
		if err := t.walkLocked(ref, false, visited); err != nil {
			t.log.Error("血缘追溯被拒绝", "方向", dir, "原因", err.Error())
			return nil, err
		}
	}

	edges, err := t.collectEdgesLocked(visited)
	if err != nil {
		t.log.Error("血缘追溯被拒绝", "方向", dir, "原因", err.Error())
		return nil, err
	}

	g := t.buildGraphLocked(ref, visited, edges)
	t.log.Info("血缘追溯", "对象", ref.ID, "版本", ref.Version, "方向", dir,
		"节点数", len(g.Nodes), "边数", len(g.Edges),
		"判定依据", "可达闭包 + 版本有效性 + 派生节点上游不缺失 + 上下游边双向闭合")
	return g, nil
}

// walkLocked 沿上游（upstream=true）或下游做可达闭包遍历，过程中即时校验。
func (t *Tracker) walkLocked(start Ref, upstream bool, reached map[string]*Node) error {
	queue := []Ref{start}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		node := t.nodes[cur.ID][cur.Version]
		if node == nil {
			return reject(ErrRefNotFound, "trace", &cur, "可达血缘引用了不存在的版本")
		}

		var neighbors []string
		if upstream {
			neighbors = mapKeys(t.in[cur.ID][cur.Version])
		} else {
			neighbors = mapKeys(t.out[cur.ID][cur.Version])
		}

		if err := t.crossCheckLocked(cur, node, upstream, neighbors); err != nil {
			return err
		}

		for _, k := range neighbors {
			next := parseRefKey(k)
			from, to := cur, next
			if upstream {
				from, to = next, cur
			}
			if err := t.checkEdgeLocked(from, to); err != nil {
				return err
			}
			if _, seen := reached[k]; !seen {
				reached[k] = t.nodes[next.ID][next.Version]
				queue = append(queue, next)
			}
		}
	}
	return nil
}

// collectEdgesLocked 收集两端都在可达集合内的边，并逐条执行
// 双向闭合、存在性与版本有效性校验。
func (t *Tracker) collectEdgesLocked(visited map[string]*Node) ([]Edge, error) {
	edgeSet := map[string]Edge{}
	for _, n := range visited {
		ref := Ref{ID: n.ID, Version: n.Version}
		for _, k := range mapKeys(t.in[n.ID][n.Version]) {
			from := parseRefKey(k)
			if _, ok := visited[k]; !ok {
				continue
			}
			if err := t.checkEdgeLocked(from, ref); err != nil {
				return nil, err
			}
			edgeSet[k+"->"+refKey(ref)] = Edge{From: from, To: ref, Op: n.Operation, Active: true}
		}
		for _, k := range mapKeys(t.out[n.ID][n.Version]) {
			to := parseRefKey(k)
			if _, ok := visited[k]; !ok {
				continue
			}
			toNode := t.nodes[to.ID][to.Version]
			if err := t.checkEdgeLocked(ref, to); err != nil {
				return nil, err
			}
			edgeSet[refKey(ref)+"->"+k] = Edge{From: ref, To: to, Op: toNode.Operation, Active: true}
		}
	}

	keys := make([]string, 0, len(edgeSet))
	for k := range edgeSet {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	edges := make([]Edge, 0, len(keys))
	for _, k := range keys {
		edges = append(edges, edgeSet[k])
	}
	return edges, nil
}

func (t *Tracker) buildGraphLocked(root Ref, visited map[string]*Node, edges []Edge) *Graph {
	keys := make([]string, 0, len(visited))
	for k := range visited {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	nodes := make([]Node, 0, len(keys))
	for _, k := range keys {
		n := visited[k]
		nodeCopy := *n
		nodeCopy.Inputs = append([]Ref(nil), n.Inputs...)
		nodes = append(nodes, nodeCopy)
	}
	return &Graph{Root: root, Nodes: nodes, Edges: edges}
}

// crossCheckLocked 对单个节点做血缘完整性交叉校验：
//   - 向上：派生节点必须记录至少一个输入，且“节点输入记录 ↔ 上游索引”一致（不漏上游）；
//   - 向下：全库任一消费方记录了该节点输入时，下游索引必须存在反向边（不漏下游）。
func (t *Tracker) crossCheckLocked(cur Ref, node *Node, upstream bool, neighbors []string) error {
	if upstream {
		if node.Kind == Derived && len(node.Inputs) == 0 {
			return reject(ErrMissingUpstream, "trace", &cur,
				"派生节点 "+refKey(cur)+" 缺少上游血缘边")
		}
		if node.Kind != Derived {
			return nil
		}
		for _, in := range node.Inputs {
			if !t.in[cur.ID][cur.Version][refKey(in)] {
				return reject(ErrMissingUpstream, "trace", &cur,
					"派生节点 "+refKey(cur)+" 记录的输入 "+refKey(in)+" 在上游索引中缺失")
			}
		}
		for _, k := range neighbors {
			found := false
			for _, in := range node.Inputs {
				if refKey(in) == k {
					found = true
					break
				}
			}
			if !found {
				return reject(ErrMissingUpstream, "trace", &cur,
					"上游索引存在边 "+k+" 但派生节点输入记录缺失")
			}
		}
		return nil
	}

	for consumerID, byVer := range t.in {
		for consumerVer, ins := range byVer {
			consumerRef := Ref{ID: consumerID, Version: consumerVer}
			if ins[refKey(cur)] && !t.out[cur.ID][cur.Version][refKey(consumerRef)] {
				return reject(ErrMissingDownstream, "trace", &cur,
					"消费方 "+refKey(consumerRef)+" 记录了输入 "+refKey(cur)+
						" 但下游索引中缺少反向边")
			}
		}
	}
	return nil
}

// checkEdgeLocked 校验单条血缘边：
// 双向索引闭合（缺上游 / 漏下游）、端点存在、两端均为当前版本（不过期）。
func (t *Tracker) checkEdgeLocked(from, to Ref) error {
	if t.nodes[from.ID] == nil || t.nodes[from.ID][from.Version] == nil {
		return reject(ErrRefNotFound, "trace", &from, "血缘边上游端点版本不存在")
	}
	if t.nodes[to.ID] == nil || t.nodes[to.ID][to.Version] == nil {
		return reject(ErrRefNotFound, "trace", &to, "血缘边下游端点版本不存在")
	}
	if !t.out[from.ID][from.Version][refKey(to)] {
		return reject(ErrMissingDownstream, "trace", &from,
			"上游版本 "+refKey(from)+" 的下游血缘中缺少到 "+refKey(to)+" 的记录")
	}
	if !t.in[to.ID][to.Version][refKey(from)] {
		return reject(ErrMissingUpstream, "trace", &to,
			"派生版本 "+refKey(to)+" 的上游血缘中缺少来自 "+refKey(from)+" 的记录")
	}
	if !t.isCurrentLocked(from.ID, from.Version) {
		cur, _ := t.currentVersionLocked(from.ID)
		return reject(ErrStaleVersion, "trace", &from,
			"血缘边上游端点已过期: "+from.Version+"，当前版本 "+cur)
	}
	if !t.isCurrentLocked(to.ID, to.Version) {
		cur, _ := t.currentVersionLocked(to.ID)
		return reject(ErrStaleVersion, "trace", &to,
			"血缘边下游端点已过期: "+to.Version+"，当前版本 "+cur)
	}
	return nil
}

// Edges 返回全部已记录血缘（含失效标记），按确定性顺序排列。
func (t *Tracker) Edges() []Edge {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var edges []Edge
	for fromID, byVer := range t.out {
		for fromVer, tos := range byVer {
			from := Ref{ID: fromID, Version: fromVer}
			for k := range tos {
				to := parseRefKey(k)
				node := t.nodes[to.ID][to.Version]
				op := ""
				if node != nil {
					op = node.Operation
				}
				edges = append(edges, Edge{
					From:   from,
					To:     to,
					Op:     op,
					Active: t.edgeActiveLocked(from, to),
				})
			}
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		ki := refKey(edges[i].From) + "->" + refKey(edges[i].To)
		kj := refKey(edges[j].From) + "->" + refKey(edges[j].To)
		return ki < kj
	})
	return edges
}

func mapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
