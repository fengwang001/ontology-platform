package heating

// computeAffectedLocked 计算隔离生效后失去供热的用户入口。
//
// 在“假想状态”（目标管段泄漏 + 边界阀门关闭，叠加当前其他泄漏与
// 阀门状态）下，从隔离域及其边界触及的节点出发做局部 BFS：
// 不含热源的连通分量中、当前有热的用户即失去供热。
// 含热源的分量一旦遇到热源即提前退出，不继续扫描。
// 调用方必须持有锁。
func (n *Network) computeAffectedLocked(p *plan, domain map[*segment]struct{}, overlayClosed map[*valve]struct{}) {
	target := n.segments[p.segID]
	// active 报告管段在假想状态下是否通流。
	active := func(s *segment) bool {
		if s == target || s.leaking {
			return false
		}
		for _, e := range [2]End{EndA, EndB} {
			v := s.valves[e]
			if v == nil {
				continue
			}
			if v.state.closed() {
				return false
			}
			if _, ok := overlayClosed[v]; ok {
				return false
			}
		}
		return true
	}

	// 种子：隔离域管段的端点，以及与这些端点相连的管段的端点。
	// 假想状态只改变了这些节点附近的连通性，其余分量不受影响。
	seedSet := make(map[string]struct{})
	for d := range domain {
		for _, e := range [2]End{EndA, EndB} {
			m := d.ends[e]
			seedSet[m] = struct{}{}
			for t := range n.adj[m] {
				seedSet[t.ends[EndA]] = struct{}{}
				seedSet[t.ends[EndB]] = struct{}{}
			}
		}
	}

	// verdict 缓存“节点所在假想分量是否含热源”的判定。
	// 含热源的分量一旦确认（遇到热源节点或命中缓存）即停止扩展，
	// 并把已触及节点都标记为有热，避免重复扫描大网。
	verdict := make(map[string]bool, len(seedSet))
	affected := make(map[string]struct{})
	for _, seed := range sortedKeys(seedSet) {
		if _, ok := verdict[seed]; ok {
			continue // 分量已有结论
		}
		visited := map[string]bool{seed: true}
		queue := []string{seed}
		var component []string
		hasSource := false
		for len(queue) > 0 && !hasSource {
			x := queue[0]
			queue = queue[1:]
			p.exploredNodes++
			component = append(component, x)
			nd := n.nodes[x]
			if nd.kind == NodeSource || verdict[x] {
				// 分量含热源：无人停供，提前退出。
				hasSource = true
				continue
			}
			for _, s := range n.sortedAdj(x) {
				p.exploredSegments++
				if !active(s) {
					continue
				}
				y := s.ends[s.endAt(x).other()]
				if !visited[y] {
					visited[y] = true
					queue = append(queue, y)
				}
			}
		}
		for _, x := range component {
			verdict[x] = hasSource
		}
		if hasSource {
			continue
		}
		for _, x := range component {
			if n.nodes[x].kind == NodeUser && n.hot[x] {
				affected[x] = struct{}{}
			}
		}
	}
	p.affected = sortedKeys(affected)
}
