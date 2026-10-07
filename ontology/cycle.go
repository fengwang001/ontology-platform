package ontology

// CycleResult 是 HasCycle 的判定结果。
type CycleResult struct {
	// HasCycle 表示在调用者当前可见且可遍历的子图中是否存在环。
	HasCycle bool
	// Cycle 是环证据：当 HasCycle 为 true 时按环上的顺序列出对象 ID，
	// 首尾相连（最后一个对象存在指回第一个对象的可遍历弧）。
	// 该序列中的对象构成一个最小对象集合：去掉其中任意对象都不再成环，
	// 且不包含任何与该环无关的对象。
	Cycle []string
	// VisitedObjects 与 VisitedLinks 是内部度量（internal，不构成对
	// 调用者的语义承诺）：
	//   - VisitedObjects：第一步存在性过滤后实际纳入判定的可见对象数；
	//   - VisitedLinks：第一步之后实际检查过的、两端皆可见的去重链接数
	//     （含因缺少遍历权限被第二步排除的链接）。
	// 向图中追加调用者完全不可见的对象与链接不会改变这两个值。
	VisitedObjects int
	VisitedLinks   int

	// view 仅由 HasCycleWithView 填充，是判定所用的同一快照弧视图。
	// 不导出、不参与任何对外语义。
	view *ArcView
}

// HasCycle 判定调用者当前权限下可见且可遍历的链接构成的子图中是否存在环。
//
// 判定次序固定：
//  1. 参数非法（调用者标识为空或未登记）返回 ErrInvalidCaller；
//  2. 先剔除调用者无存在性权限的对象及其关联的全部链接；
//  3. 再剔除调用者无遍历权限的链接；
//  4. 在剩余子图上做环判定。可见对象集合为空时直接判定无环（非错误）。
//
// 环长度为一的自环计入；双向链接允许沿两个方向遍历，因此同一对对象沿
// 双向链接往返构成的长度为二的环也计入。结果只取决于当前可见子图的拓扑
// 形态：多次调用在同一权限/链接状态下返回的布尔值与证据集合逐一相同，
// 与起始对象、访问顺序、对象/链接创建历史无关。
func (g *Graph) HasCycle(caller string) (CycleResult, error) {
	return g.hasCycleImpl(caller, false)
}

// HasCycleWithView 在同一个一致快照内完成判定，并返回该快照的弧视图，
// 便于调用方/内部验证证据与判定来自同一时刻。弧视图仅供诊断，不改变
// HasCycle 的任何语义。
func (g *Graph) HasCycleWithView(caller string) (CycleResult, *ArcView, error) {
	res, err := g.hasCycleImpl(caller, true)
	if err != nil {
		return CycleResult{}, nil, err
	}
	return res, res.view, nil
}

func (g *Graph) hasCycleImpl(caller string, withView bool) (CycleResult, error) {
	g.mu.RLock()
	snap, ok := g.takeSnapshot(caller)
	g.mu.RUnlock()
	if !ok {
		return CycleResult{}, ErrInvalidCaller
	}

	res := CycleResult{
		VisitedObjects: snap.count,
		VisitedLinks:   snap.candidateLinks,
	}
	if withView {
		view := &ArcView{Objects: snap.objects, arcs: map[string]map[string]struct{}{}}
		for from, tos := range snap.out {
			view.arcs[from] = map[string]struct{}{}
			for _, to := range tos {
				view.arcs[from][to] = struct{}{}
			}
		}
		res.view = view
	}
	if snap.count == 0 {
		return res, nil
	}

	cycle := findCanonicalCycle(snap)
	if cycle != nil {
		res.HasCycle = true
		res.Cycle = cycle
	}
	return res, nil
}

// findCanonicalCycle 在快照的有向弧集合中确定性地找出一个环证据。
//
// 确定性规则（与遍历起始对象、邻接访问顺序无关）：
//   - 候选只取简单有向环（顶点不重复，自环长度 1、双向往返长度 2）；
//   - 先按长度升序（最短环）；
//   - 长度相同则比较环的“规范表示”——以序列中最小 ID 为起点的唯一
//     旋转——取字典序最小者；
//   - 起点按对象 ID 升序枚举，邻接表同样按 ID 升序，因此第一次达到
//     “某长度下的最优”时即可确定全局最优。
func findCanonicalCycle(s *snapshot) []string {
	var best []string
	for _, root := range s.objects {
		cand := shortestCycleFrom(s, root)
		if cand == nil {
			continue
		}
		if best == nil ||
			len(cand) < len(best) ||
			(len(cand) == len(best) && lessCycle(cand, best)) {
			best = cand
		}
	}
	if best == nil {
		return nil
	}
	// 统一以环中最小 ID 为起点的旋转输出，使证据表示本身也与遍历顺序无关。
	return canonicalRotation(best)
}

// shortestCycleFrom 以 root 为环上最小 ID 的约束，用 BFS 找出从 root
// 出发回到 root 的最短简单环。BFS 的父指针路径天然是简单路径；由于
// 邻接表已排序，相同长度的环中 BFS 首先完成的即是字典序最小者。
func shortestCycleFrom(s *snapshot, root string) []string {
	dist := map[string]int{root: 0}
	parent := map[string]string{}
	queue := []string{root}
	foundLen := -1
	var foundEnd string

	for head := 0; head < len(queue) && foundLen == -1; head++ {
		u := queue[head]
		for _, v := range s.out[u] {
			if v == root {
				// 自环（u==root, len 1）与回到起点的闭合边。
				foundLen = dist[u] + 1
				foundEnd = u
				break
			}
			if _, seen := dist[v]; !seen {
				dist[v] = dist[u] + 1
				parent[v] = u
				queue = append(queue, v)
			}
		}
	}
	if foundLen == -1 {
		return nil
	}

	// 由父指针还原 root -> ... -> foundEnd，再由 foundEnd 闭合回 root。
	cycle := make([]string, 0, foundLen)
	cycle = append(cycle, root)
	path := make([]string, 0, foundLen-1)
	for cur := foundEnd; cur != root; cur = parent[cur] {
		path = append(path, cur)
	}
	for i := len(path) - 1; i >= 0; i-- {
		cycle = append(cycle, path[i])
	}
	return cycle
}

// canonicalRotation 返回环序列以最小 ID 为起点的唯一旋转表示。
func canonicalRotation(cycle []string) []string {
	minIdx := 0
	for i := 1; i < len(cycle); i++ {
		if cycle[i] < cycle[minIdx] {
			minIdx = i
		}
	}
	out := make([]string, 0, len(cycle))
	out = append(out, cycle[minIdx:]...)
	out = append(out, cycle[:minIdx]...)
	return out
}

// lessCycle 按规范旋转的字典序比较两个等长环。
func lessCycle(a, b []string) bool {
	ra := canonicalRotation(a)
	rb := canonicalRotation(b)
	for i := range ra {
		if ra[i] != rb[i] {
			return ra[i] < rb[i]
		}
	}
	return false
}
