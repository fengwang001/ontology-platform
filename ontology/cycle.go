package ontology

import "sort"

// detectCycle 在不可变快照上判定环，并返回规范的（canonical）证据集合。
//
// 环的定义（按弧的方向遍历）：
//   - 长度 1：自环链接（LinkType.AllowSelf 才可能建成）；
//   - 长度 2：u->v 与 v->u 两条弧同时可遍历即可。一条双向链接同时展开
//     为正反两条弧，因此单独一条双向链接就构成 u<->v 的往返环；两条
//     互相反向的有向链接同理；多重同类型链接不产生新的顶点集合，证据
//     只含 {u,v} 一次；
//   - 长度 >=3：普通有向简单环。
//
// 确定性：算法的每一步输入顺序都按 ID 排序（根序、邻接序、SCC 序、
// BFS 序），且最终在所有候选环中选择「顶点数最少；相同长度则顶点集合
// 字典序最小」的一个。因此无论调用者指定哪个起始对象（API 根本不接受
// 起始对象参数）、底层邻接如何排列，同一快照必然得到逐元素相同的证据。
// 复杂度仅与可见子图规模相关：O((Vv+Ev) * |SCC|) 以内，Vv/Ev 只统计
// 可见对象与实际读取的可遍历弧；不可见的对象/链接从不被访问。
func (s *visibleSubgraph) detectCycle() (Result, Metrics) {
	m := Metrics{}

	// 候选环统一表示为排序后的顶点集合（证据是集合，与环上的方向无关）。
	var best []string
	take := func(cycle []string) {
		cp := append([]string(nil), cycle...)
		sort.Strings(cp)
		if best == nil || len(cp) < len(best) || (len(cp) == len(best) && lexLess(cp, best)) {
			best = cp
		}
	}

	// 长度 1：自环。按对象 ID 排序后取最小者作为候选，保证多重自环可重复。
	for _, v := range s.objects {
		m.VisitedObjects++
		if loops := s.selfLoops[v]; len(loops) > 0 {
			take([]string{v})
		}
	}

	// 长度 >=2：Tarjan 强连通分量。弧按排序后的对象序驱动，
	// 只有 |SCC|>=2 的分量才可能含长度 >=2 的环。
	index := make(map[string]int, len(s.objects))
	low := make(map[string]int, len(s.objects))
	onStack := make(map[string]bool, len(s.objects))
	var stack []string
	next := 0

	var strongConnect func(v string)
	strongConnect = func(v string) {
		index[v] = next
		low[v] = next
		next++
		m.VisitedObjects++
		stack = append(stack, v)
		onStack[v] = true

		for _, a := range s.adjacency[v] {
			m.VisitedLinks++
			if _, seen := index[a.to]; !seen {
				strongConnect(a.to)
				if low[a.to] < low[v] {
					low[v] = low[a.to]
				}
			} else if onStack[a.to] && index[a.to] < low[v] {
				low[v] = index[a.to]
			}
		}

		if low[v] == index[v] {
			var scc []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				scc = append(scc, w)
				if w == v {
					break
				}
			}
			sort.Strings(scc)
			if len(scc) >= 2 {
				if cyc := s.shortestCycleInSCC(scc, &m); cyc != nil {
					take(cyc)
				}
			}
		}
	}

	for _, v := range s.objects { // 根序确定：按 ID 排序
		if _, seen := index[v]; !seen {
			strongConnect(v)
		}
	}

	if best == nil {
		return Result{HasCycle: false}, m
	}
	return Result{HasCycle: true, Evidence: best}, m
}

// shortestCycleInSCC 在给定强连通分量内返回规范的最短简单环：
// 对分量中（按 ID 排序的）每个顶点求经过它的最短有向环（BFS，弧序
// 已排序保证同长路径唯一），取长度最短、同长时顶点集合字典序最小者。
// 特别地，大小为 2 的 SCC 必然存在 u->v、v->u，直接返回 {u,v}，
// 这也覆盖了“单条双向链接展开为一对反向弧”的往返环与平行边情形。
func (s *visibleSubgraph) shortestCycleInSCC(scc []string, m *Metrics) []string {
	inSCC := make(map[string]bool, len(scc))
	for _, v := range scc {
		inSCC[v] = true
	}

	var best []string
	consider := func(cyc []string) {
		sort.Strings(cyc)
		if best == nil || len(cyc) < len(best) || (len(cyc) == len(best) && lexLess(cyc, best)) {
			best = cyc
		}
	}

	for _, start := range scc {
		m.VisitedObjects++
		// parent[v] 记录 BFS 树上 v 的前驱，dist 同时充当 visited。
		dist := map[string]int{start: 0}
		parent := map[string]string{}
		queue := []string{start}
		found := false
		for len(queue) > 0 && !found {
			u := queue[0]
			queue = queue[1:]
			for _, a := range s.adjacency[u] {
				if !inSCC[a.to] {
					continue
				}
				m.VisitedLinks++
				if a.to == start && u != start {
					// 找到回到起点的非平凡环：start -> ... -> u -> start。
					cyc := []string{start}
					for x := u; x != start; x = parent[x] {
						cyc = append(cyc, x)
					}
					consider(cyc)
					found = true
					break
				}
				if _, seen := dist[a.to]; !seen {
					dist[a.to] = dist[u] + 1
					parent[a.to] = u
					queue = append(queue, a.to)
				}
			}
		}
		// 最短可能环为长度 2（本方法只处理 |SCC|>=2）；已得到长度 2
		// 的候选后，其余起点不可能产生更短环，可提前终止。
		if best != nil && len(best) == 2 {
			break
		}
	}
	return best
}

// lexLess 比较两个已排序字符串切片的字典序。
func lexLess(a, b []string) bool {
	for i := range a {
		if i >= len(b) {
			return false
		}
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}
