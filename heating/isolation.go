package heating

import "sort"

// plan 为推演的内部结果，比公开的 Plan 多携带边界所需关闭阀门全集，
// 供执行隔离时建立引用计数。
type plan struct {
	segID    string
	toClose  []string // 需要新关闭的阀门（有序）
	needs    []string // 边界上全部要求关闭的阀门（有序，含已关闭者）
	domain   []string // 隔离域管段（有序）
	affected []string // 失去供热的用户入口（有序）

	exploredSegments int
	exploredNodes    int
}

// public 转为对外暴露的推演结果。
func (p *plan) public() *Plan {
	return &Plan{
		Segment:          p.segID,
		ValvesToClose:    append([]string(nil), p.toClose...),
		Domain:           append([]string(nil), p.domain...),
		AffectedUsers:    append([]string(nil), p.affected...),
		ExploredSegments: p.exploredSegments,
		ExploredNodes:    p.exploredNodes,
	}
}

// SimulateIsolation 对一条管段做隔离推演。只读，结果对同一状态唯一。
func (n *Network) SimulateIsolation(segID string) (*Plan, error) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if segID == "" {
		return nil, ErrInvalidParam
	}
	if _, ok := n.segments[segID]; !ok {
		return nil, ErrSegmentNotFound
	}
	p, err := n.simulateLocked(segID)
	if err != nil {
		return nil, err
	}
	return p.public(), nil
}

// simulateLocked 计算隔离域与需关闭阀门。调用方必须持有锁且已校验管段存在。
//
// 算法分两阶段，保证结果对同一状态唯一（与扩展顺序无关）：
//
// 第一阶段求隔离域（最小不动点）：从目标管段出发，对域内管段的每一端：
//   - 该端已关闭（关/卡死在关）：此方向本就不相通，跳过；
//   - 该端位于热源节点：不在此扩展（热源端由域内管段自身阀门封闭，见第二阶段）；
//   - 否则对每个相通的邻接管段：其在此端阀门缺失或卡死在开时并入隔离域。
//
// 第二阶段对最终隔离域的边界收集需关闭的阀门：
//   - 域内管段位于热源节点的端：必须关闭该端自身阀门，缺失或卡死在开则无法隔离；
//   - 其余边界：域外邻接管段在此端的阀门，开则列入待关集合，
//     已关/卡死在关视为已满足（不重复列入待关集合）。
//
// 只访问隔离域及其边界上的管段，开销与全网规模无关。
func (n *Network) simulateLocked(segID string) (*plan, error) {
	target := n.segments[segID]
	domain := map[*segment]struct{}{target: {}}
	exploredSegments := 0
	exploredNodes := 0

	// 第一阶段：扩展隔离域。
	queue := []*segment{target}
	for len(queue) > 0 {
		d := queue[0]
		queue = queue[1:]
		exploredSegments++
		for _, e := range [2]End{EndA, EndB} {
			m := d.ends[e]
			exploredNodes++
			if d.endClosed(e) {
				// 此端已关闭，与该节点处的任何管段都不相通。
				continue
			}
			if n.nodes[m].kind == NodeSource {
				continue
			}
			for _, t := range n.sortedAdj(m) {
				exploredSegments++
				if _, ok := domain[t]; ok {
					continue
				}
				tv := t.valves[t.endAt(m)]
				if tv == nil || tv.state == ValveStuckOpen {
					// 此处无法关闭：邻接管段并入隔离域。
					domain[t] = struct{}{}
					queue = append(queue, t)
				}
			}
		}
	}

	// 第二阶段：对最终隔离域的边界收集需关闭的阀门。
	needClosed := make(map[*valve]struct{})
	for d := range domain {
		for _, e := range [2]End{EndA, EndB} {
			m := d.ends[e]
			exploredNodes++
			if d.endClosed(e) {
				continue
			}
			if n.nodes[m].kind == NodeSource {
				// 域扩展到热源节点所连的管段端：只能关闭域内管段
				// 自身这一端（关闭邻接管段挡不住热源直接注入）。
				v := d.valves[e]
				if v == nil || v.state == ValveStuckOpen {
					return nil, ErrNotIsolatable
				}
				needClosed[v] = struct{}{}
				continue
			}
			for _, t := range n.sortedAdj(m) {
				exploredSegments++
				if _, ok := domain[t]; ok {
					continue
				}
				// 此端阀门必然存在且非卡死在开，否则 t 已在域中。
				needClosed[t.valves[t.endAt(m)]] = struct{}{}
			}
		}
	}

	p := &plan{segID: segID}
	needsSet := make(map[string]struct{}, len(needClosed))
	toCloseSet := make(map[string]struct{})
	for v := range needClosed {
		needsSet[v.id] = struct{}{}
		if v.state == ValveOpen {
			toCloseSet[v.id] = struct{}{}
		}
	}
	p.needs = sortedKeys(needsSet)
	p.toClose = sortedKeys(toCloseSet)
	domainIDs := make(map[string]struct{}, len(domain))
	for s := range domain {
		domainIDs[s.id] = struct{}{}
	}
	p.domain = sortedKeys(domainIDs)
	p.exploredSegments = exploredSegments
	p.exploredNodes = exploredNodes

	// 假想状态下计算停供影响：边界阀门视为已关，目标管段视为泄漏。
	overlay := make(map[*valve]struct{}, len(toCloseSet))
	for id := range toCloseSet {
		overlay[n.valves[id]] = struct{}{}
	}
	n.computeAffectedLocked(p, domain, overlay)
	return p, nil
}

// sortedAdj 返回节点的关联管段（按 id 排序），保证推演计数确定。
func (n *Network) sortedAdj(nodeID string) []*segment {
	set := n.adj[nodeID]
	out := make([]*segment, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

// sortedKeys 返回有序键切片，保证推演结果对同一状态唯一。
func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
