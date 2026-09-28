package reachability

// addEdgeLocked 在调用方已持有写锁、且边 (a,b) 从不存在变为存在时执行。
//
// 设新边加入前 a 的前驱集合（含 a 自身）为 S，b 的后继集合（含 b 自身）为 T，
// 则新出现的可达点对恰好是 S×T：任何首次出现的路径都要经过新边，
// 其前缀终点为 a、后缀起点为 b（自环 / 环导致的反复经过新边仍落在该集合内）。
func (g *Graph) addEdgeLocked(p Pair) {
	a, b := p.From, p.To

	addSet(g.edgeOut, a, b)
	addSet(g.edgeIn, b, a)

	sources := make([]string, 0, 1+len(g.in[a]))
	sources = append(sources, a)
	for s := range g.in[a] {
		sources = append(sources, s)
	}
	targets := make([]string, 0, 1+len(g.out[b]))
	targets = append(targets, b)
	for t := range g.out[b] {
		targets = append(targets, t)
	}

	for _, s := range sources {
		for _, t := range targets {
			g.addPairLocked(s, t)
		}
	}
}

// removeEdgeLastLocked 在边 (a,b) 重数即将归零、需要物理删除该边时维护闭包。
//
// 步骤：
//  1. 标出全部“可能”受影响的点对 S×T（S = {a}∪in[a]，T = {b}∪out[b]，
//     基于删除前的闭包），一次性从可达索引中移除；
//  2. 物理删除该边及其邻接索引；
//   3. 在剩余图上对被移除的点对重新推导：一条点对仍可达，当且仅当存在
//     剩余直连边，或存在边 (s,w) 使 (w,t) 已在闭包中。推导按闭包增长
//     级联重排候选，直到不动点；最终仍无法推导的点对即为真正失效的点对。
//
// 该“先全部摘除、再重新推导”的策略保证删边既不过删（仍可达的全部加回）
// 也不漏算（任何依赖被删边的点对都已先被摘除）。
func (g *Graph) removeEdgeLastLocked(p Pair) {
	a, b := p.From, p.To

	// 先把邻接集合复制成切片：摘除点对会改写 g.in / g.out，不能边遍历边删除。
	sources := make([]string, 0, 1+len(g.in[a]))
	sources = append(sources, a)
	for s := range g.in[a] {
		sources = append(sources, s)
	}
	targets := make([]string, 0, 1+len(g.out[b]))
	targets = append(targets, b)
	for t := range g.out[b] {
		targets = append(targets, t)
	}

	affected := make(map[Pair]struct{}, len(sources)*len(targets))
	for _, s := range sources {
		for _, t := range targets {
			pair := Pair{From: s, To: t}
			if _, ok := affected[pair]; ok {
				continue
			}
			affected[pair] = struct{}{}
			g.removePairLocked(s, t)
		}
	}

	// 物理删除边。
	delete(g.mult, p)
	removeSet(g.edgeOut, a, b)
	removeSet(g.edgeIn, b, a)

	// missing：被摘除且尚未重新推导成立的点对。
	missing := make(map[Pair]struct{}, len(affected))
	for pair := range affected {
		missing[pair] = struct{}{}
	}

	justified := func(s, t string) bool {
		// 剩余图上存在直连边。
		if _, ok := g.edgeOut[s][t]; ok {
			return true
		}
		// 存在边 (s,w)，且 (w,t) 当前已在闭包中（路径长度至少为 1，
		// 与直连边拼接后即为长度至少为 2 的见证路径）。
		for w := range g.edgeOut[s] {
			if _, ok := g.out[w][t]; ok {
				return true
			}
		}
		return false
	}

	queue := make([]Pair, 0, len(missing))
	for pair := range missing {
		queue = append(queue, pair)
	}

	for i := 0; i < len(queue); i++ {
		pair := queue[i]
		if _, ok := missing[pair]; !ok {
			continue
		}
		s, t := pair.From, pair.To
		if !justified(s, t) {
			continue
		}
		g.addPairLocked(s, t)
		delete(missing, pair)

		// (s,t) 重新成立可能进一步证明：
		//   (x,t)：存在剩余边 (x,s)，x 为 s 的前驱；
		//   (s,y)：存在剩余边 (t,y)，y 为 t 的后继。
		for x := range g.edgeIn[s] {
			c := Pair{From: x, To: t}
			if _, ok := missing[c]; ok {
				queue = append(queue, c)
			}
		}
		for y := range g.edgeOut[t] {
			c := Pair{From: s, To: y}
			if _, ok := missing[c]; ok {
				queue = append(queue, c)
			}
		}
	}
	// queue 耗尽后仍留在 missing 中的点对确属失效，保持已摘除状态即可。
}

// addPairLocked 将可达点对 (u, v) 加入 out/in 双向索引，返回是否为新点对。
func (g *Graph) addPairLocked(u, v string) bool {
	if _, ok := g.out[u][v]; ok {
		return false
	}
	addSet(g.out, u, v)
	addSet(g.in, v, u)
	return true
}

// removePairLocked 将可达点对 (u, v) 从 out/in 双向索引移除，空集合一并清理。
func (g *Graph) removePairLocked(u, v string) {
	removeSet(g.out, u, v)
	removeSet(g.in, v, u)
}

// addSet 在 m[key] 集合中插入 val，外层 map 与内层集合按需创建。
func addSet(m map[string]map[string]struct{}, key, val string) {
	s, ok := m[key]
	if !ok {
		s = make(map[string]struct{})
		m[key] = s
	}
	s[val] = struct{}{}
}

// removeSet 从 m[key] 集合中删除 val，内层集合空时连同外层键一起清理。
func removeSet(m map[string]map[string]struct{}, key, val string) {
	s, ok := m[key]
	if !ok {
		return
	}
	delete(s, val)
	if len(s) == 0 {
		delete(m, key)
	}
}
