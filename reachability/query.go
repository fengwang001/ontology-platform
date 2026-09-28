package reachability

// reachableLocked 报告 u 是否可达 v，调用方至少持有读锁。
// 以 out 索引判定：out[u] 中存在 v。
func (g *Graph) reachableLocked(u, v string) bool {
	return false
}

// explainLocked 给出 from 到 to 的判定依据，调用方至少持有读锁。
// 可达时在当前边集合上 BFS，返回一条长度至少为 2 的见证路径；
// 不可达时返回 BFS 未能到达的说明。
func (g *Graph) explainLocked(from, to string) (bool, []string, string) {
	return false, nil, ""
}

// pairsLocked 收集全部可达点对，调用方至少持有读锁。
// 结果按 (From, To) 字典序排序，保证确定性。
func (g *Graph) pairsLocked() []Pair {
	return nil
}

// edgesLocked 收集全部重数为正的边，调用方至少持有读锁。
// 结果按 (From, To) 字典序排序。
func (g *Graph) edgesLocked() []Edge {
	return nil
}
