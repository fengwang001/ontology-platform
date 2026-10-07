package aggview

// graph 是遍历所需的只读邻接视图，由 Engine 基于 ontology.Store 提供。
// neighbors 返回实例 from 沿关系 rel 出发、且终点对象类型属于该跳允许
// 类型集合的全部终点实例。并行链接（from,to 相同的多条 Link）只贡献一次。
type graph interface {
	neighbors(from, rel string, allowed map[string]struct{}) []string
}

// reachable 返回从 src 出发沿路径恰好行走 k 跳后能够到达的"不同"实例
// 集合（ID 去重），且只统计路径每一跳类型集合允许的实例。
//
// 环处理：按层 BFS，每层都做集合去重。即使图中存在环、实例能通过任意
// 多条走法（walk）到达同一节点，该节点在本层也只出现一次，因此不会无限
// 展开，也不会指数膨胀。深度恰为跳数，因此环上"本身是终点类型"的实例
// 只要在第 k 层可达就被计入一次，且仅一次。
//
// 时间复杂度：O(k * (访问到的实例数 + 相关链接数))；与"走法数量"无关，
// 只与去重后的可达子图规模相关。
func reachable(g graph, p *viewPath, src string) map[string]struct{} {
	current := map[string]struct{}{src: {}}
	for i := 0; i < p.Len(); i++ {
		next := map[string]struct{}{}
		h := p.hops[i]
		for node := range current {
			for _, nb := range g.neighbors(node, h.relation, h.types) {
				next[nb] = struct{}{}
			}
		}
		current = next
		if len(current) == 0 {
			return current
		}
	}
	return current
}

// reverseReachable 返回"沿路径前 i 跳（0..i-1）能够到达 node 的全部起点
// 类型实例 ID"。用于链接增删时精确圈定受影响的起点候选集合。
//
// 实现方式：在反向图上按层 BFS i 跳，起点层为 {node}，第 i 层结果即
// 所有能够正向 i 跳到达 node 的实例。环与重复走法同样由每层集合去重处理。
type reverseGraph interface {
	reverseNeighbors(to, rel string) []string
}

// sourcesReaching 返回沿前 hops 跳能在恰好 hops 步到达 node 的实例集合。
// 类型约束：反向撤销正向第 j 跳时，找到的前驱必须属于"第 j 层"的类型，
// 即起点类型（j==0）或第 j-1 跳的终点类型集合（j>0）。
// typeOK 由调用方（Engine）依据存储中的实例类型判定。
func sourcesReaching(rg reverseGraph, p *viewPath, hops int, node string,
	typeOK func(level int, id string) bool) map[string]struct{} {
	current := map[string]struct{}{node: {}}
	// 反向走 hops 步：第 step 步撤销正向第 (hops-1-step) 跳。
	for step := 0; step < hops; step++ {
		j := hops - 1 - step
		h := p.hops[j]
		next := map[string]struct{}{}
		for n := range current {
			for _, pred := range rg.reverseNeighbors(n, h.relation) {
				// pred 位于正向第 j 层，必须满足该层类型约束。
				if typeOK != nil && !typeOK(j, pred) {
					continue
				}
				// 同层去重：反向 BFS 与前向一样按层集合推进，
				// 环上的实例不会导致重复展开或无限循环。
				next[pred] = struct{}{}
			}
		}
		current = next
		if len(current) == 0 {
			return current
		}
	}
	return current
}

// cycleIntroduced 判断"在第 i 跳加入一条 from->to 的链接"是否会让某个
// 能够 i 跳到达 from 的起点实例在沿路径行走时重复经过自身（出现环），
// 而该环又无法被声明阶段的静态分析排除。
//
// 判定（保守且局部）：
//   - 若路径声明 staticallyCycleFree，环在类型层面已不可能，直接返回 false；
//   - 否则检查 from 与 to 是否属于同一对象类型（针对自反类型集合这一最
//     常见、可精确判定的运行时环），并且存在起点能 i 跳到达 from：
//     加入该链接后 from 可再走一步到同类型的 to，形成运行时自环结构，
//     无法保证定长行走终止语义时拒绝本次变更。
//
// 类型不相交的多类型环由声明阶段的静态分析排除；本函数只处理静态无法
// 排除的剩余情形，属于需求中"运行时拒绝某次变更"的错误类别。
func (e *Engine) cycleIntroduced(p *viewPath, i int, from, to string) bool {
	if p.staticallyCycleFree() {
		return false
	}
	ft, ok1 := e.store.ObjectTypeOfLocked(from)
	tt, ok2 := e.store.ObjectTypeOfLocked(to)
	if !ok1 || !ok2 || ft != tt {
		return false
	}
	if !p.allowsType(i, tt) {
		return false
	}
	// 存在任何起点能 i 跳到达 from，则该环对视图实际可达性产生影响。
	preds := sourcesReaching(e, p, i, from, e.levelTypeOK(p))
	for range preds {
		return true
	}
	return false
}
