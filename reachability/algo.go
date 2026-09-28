package reachability

import "sort"

// sortedNeighbors 返回按字典序排列的后继，保证 BFS 见证路径确定性。
func sortedNeighbors(u string, adj map[string]map[string]struct{}) []string {
	ns := make([]string, 0, len(adj[u]))
	for v := range adj[u] {
		ns = append(ns, v)
	}
	sort.Strings(ns)
	return ns
}

// 本文件只包含不持有任何状态的图算法工具：
//   - bfsReach：单源长度至少为 1 的可达集合
//   - bfsReachReverse：能到达指定源点的点集（反向 BFS）
//   - bfsPath：两点间一条最短有向路径（见证路径）
//   - shortestCycleFrom：回到自身的最短有向环
//   - fullClosure：对每个源点做朴素遍历得到全量可达点对
//
// adj 以邻接集合表示：adj[u] 含 v 当且仅当存在一条重数为正的边 u->v。

// bfsReach 返回从 start 出发经长度至少为 1 的有向路径可达的全部节点。
func bfsReach(start string, adj map[string]map[string]struct{}) map[string]struct{} {
	seen := make(map[string]struct{})
	frontier := []string{start}
	seen[start] = struct{}{}
	for len(frontier) > 0 {
		u := frontier[0]
		frontier = frontier[1:]
		for v := range adj[u] {
			if _, ok := seen[v]; !ok {
				seen[v] = struct{}{}
				frontier = append(frontier, v)
			}
		}
	}
	delete(seen, start) // 可达要求路径长度 >= 1，先去掉起点自身
	// 若存在自环或经环回到起点，BFS 不会重新加入 start，需要单独检查。
	if reachesSelf(start, adj) {
		seen[start] = struct{}{}
	}
	return seen
}

// reachesSelf 判断是否存在从 start 出发、长度 >= 1 且回到 start 的路径。
func reachesSelf(start string, adj map[string]map[string]struct{}) bool {
	for v := range adj[start] {
		if v == start {
			return true
		}
	}
	// 从 start 的直接后继出发做 BFS，能回到 start 即存在环。
	visited := map[string]struct{}{start: {}}
	frontier := make([]string, 0, len(adj[start]))
	for v := range adj[start] {
		if v == start {
			return true
		}
		visited[v] = struct{}{}
		frontier = append(frontier, v)
	}
	for len(frontier) > 0 {
		u := frontier[0]
		frontier = frontier[1:]
		if _, back := adj[u][start]; back {
			return true
		}
		for v := range adj[u] {
			if _, ok := visited[v]; !ok {
				visited[v] = struct{}{}
				frontier = append(frontier, v)
			}
		}
	}
	return false
}

// bfsReachReverse 返回在有向图中能到达 start 的全部点（即在反向图上
// 从 start 出发长度 >= 1 可达的点），不含 start 自身。
func bfsReachReverse(start string, adj map[string]map[string]struct{}) map[string]struct{} {
	rev := make(map[string]map[string]struct{})
	for u, ns := range adj {
		for v := range ns {
			m, ok := rev[v]
			if !ok {
				m = map[string]struct{}{}
				rev[v] = m
			}
			m[u] = struct{}{}
		}
	}
	seen := map[string]struct{}{}
	frontier := []string{start}
	for len(frontier) > 0 {
		u := frontier[0]
		frontier = frontier[1:]
		for v := range rev[u] {
			if _, ok := seen[v]; ok || v == start {
				continue
			}
			seen[v] = struct{}{}
			frontier = append(frontier, v)
		}
	}
	return seen
}

// bfsPath 返回从 from 到 to 的一条最短有向路径（顶点序列）。
// from == to 时返回单元素切片 [from]；不可达时返回 nil。
func bfsPath(from, to string, adj map[string]map[string]struct{}) []string {
	if from == to {
		return []string{from}
	}
	prev := make(map[string]string)
	visited := map[string]struct{}{from: {}}
	frontier := []string{from}
	found := false
	for len(frontier) > 0 && !found {
		u := frontier[0]
		frontier = frontier[1:]
		for _, v := range sortedNeighbors(u, adj) {
			if _, ok := visited[v]; ok {
				continue
			}
			visited[v] = struct{}{}
			prev[v] = u
			if v == to {
				found = true
				break
			}
			frontier = append(frontier, v)
		}
	}
	if _, ok := visited[to]; !ok {
		return nil
	}
	path := []string{to}
	for cur := to; cur != from; {
		cur = prev[cur]
		path = append(path, cur)
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}

// shortestCycleFrom 返回从 start 出发回到 start 的最短有向环（顶点序列，
// 首尾均为 start，长度 >= 2 个顶点）；不存在时返回 nil。
func shortestCycleFrom(start string, adj map[string]map[string]struct{}) []string {
	// 自环即为长度 1 的环。
	if _, self := adj[start][start]; self {
		return []string{start, start}
	}
	// 多源 BFS：记录每个后继由哪条首边到达，回溯成 start->...->start。
	prev := make(map[string]string)
	first := make(map[string]string)
	visited := map[string]struct{}{}
	var frontier []string
	for _, v := range sortedNeighbors(start, adj) {
		if _, ok := visited[v]; ok {
			continue
		}
		visited[v] = struct{}{}
		prev[v] = start
		first[v] = v
		frontier = append(frontier, v)
	}
	var end string
	found := false
	for len(frontier) > 0 && !found {
		u := frontier[0]
		frontier = frontier[1:]
		if _, back := adj[u][start]; back {
			end = u
			found = true
			break
		}
		for _, v := range sortedNeighbors(u, adj) {
			if v == start || first[v] != "" {
				continue
			}
			prev[v] = u
			first[v] = first[u]
			frontier = append(frontier, v)
		}
	}
	if !found {
		return nil
	}
	path := []string{start}
	var rev []string
	for cur := end; cur != start; cur = prev[cur] {
		rev = append(rev, cur)
	}
	for i := len(rev) - 1; i >= 0; i-- {
		path = append(path, rev[i])
	}
	path = append(path, start)
	return path
}

// fullClosure 从每个节点出发朴素遍历，返回全量可达点对（路径长度 >= 1）。
// 这是“朴素遍历”基准：增量维护的结果必须始终与之一致。
func fullClosure(nodes []string, adj map[string]map[string]struct{}) map[string]map[string]struct{} {
	reach := make(map[string]map[string]struct{}, len(nodes))
	for _, u := range nodes {
		dests := bfsReach(u, adj)
		if len(dests) > 0 {
			reach[u] = dests
		}
	}
	return reach
}

// nodeUniverse 返回正/逆向邻接中出现过的全部节点。
func nodeUniverse(out, in map[string]map[string]struct{}) []string {
	all := make(map[string]struct{})
	for u := range out {
		all[u] = struct{}{}
	}
	for u := range in {
		all[u] = struct{}{}
	}
	nodes := make([]string, 0, len(all))
	for u := range all {
		nodes = append(nodes, u)
	}
	return nodes
}
