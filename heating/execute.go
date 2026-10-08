package heating

import "sort"

// ExecuteIsolation 执行隔离：关闭推演给出的全部阀门并把管段标记为泄漏中。
//
// 全有或全无：expected 为调用方此前推演得到的待关阀门集合（可为 nil 表示
// 不校验）；若执行时重新推演的结果与 expected 不一致，说明有阀门状态已
// 发生变化，整体拒绝，拓扑、阀门与泄漏标记均不变。
//
// 错误次序：参数非法 > 管段不存在 > 已处于活动隔离 > 无法隔离 > 阀门状态已变化。
func (n *Network) ExecuteIsolation(segID string, expected []string) (*Plan, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if segID == "" {
		return nil, ErrInvalidParam
	}
	s, ok := n.segments[segID]
	if !ok {
		return nil, ErrSegmentNotFound
	}
	if _, ok := n.isolations[segID]; ok {
		return nil, ErrSegmentIsolated
	}
	p, err := n.simulateLocked(segID)
	if err != nil {
		return nil, err
	}
	if expected != nil && !sameStringSet(p.toClose, expected) {
		return nil, ErrValveStateChanged
	}
	// 推演在同一临界区内完成，此处阀门必然仍为开，可安全关闭。
	closed := make(map[string]struct{}, len(p.toClose))
	for _, id := range p.toClose {
		v := n.valves[id]
		v.state = ValveClosed
		v.closedByIsolation = true
		closed[id] = struct{}{}
	}
	needs := make(map[string]struct{}, len(p.needs))
	for _, id := range p.needs {
		needs[id] = struct{}{}
	}
	s.leaking = true
	n.isolations[segID] = &isolationRecord{segID: segID, needs: needs, closed: closed}
	n.recomputeHotLocked()
	return p.public(), nil
}

// CompleteRepair 修复完成：撤销泄漏标记，并把该次隔离关闭的阀门恢复为开。
//
// 仍被其他活动隔离需要的阀门保持关闭；隔离之外被手工关闭的阀门不得恢复；
// 已被现场上报为卡死在关的阀门保持卡死状态。
//
// 错误次序：参数非法 > 管段不存在 > 未处于隔离。
func (n *Network) CompleteRepair(segID string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if segID == "" {
		return ErrInvalidParam
	}
	s, ok := n.segments[segID]
	if !ok {
		return ErrSegmentNotFound
	}
	rec, ok := n.isolations[segID]
	if !ok {
		return ErrSegmentNotIsolated
	}
	s.leaking = false
	delete(n.isolations, segID)

	// 统计其余活动隔离对阀门的需要。
	stillNeeded := make(map[string]struct{})
	for _, other := range n.isolations {
		for id := range other.needs {
			stillNeeded[id] = struct{}{}
		}
	}
	for id := range rec.needs {
		if _, ok := stillNeeded[id]; ok {
			continue
		}
		v := n.valves[id]
		if v == nil {
			continue // 阀门已被拆除
		}
		// 只恢复“由隔离关闭且当前确为关”的阀门：
		// 手工关闭（closedByIsolation 为假）与卡死状态都保持不变。
		if v.state == ValveClosed && v.closedByIsolation {
			v.state = ValveOpen
			v.closedByIsolation = false
		}
	}
	n.recomputeHotLocked()
	return nil
}

// sameStringSet 比较两个字符串集合是否相同（b 允许无序、可含重复）。
func sameStringSet(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sorted := append([]string(nil), b...)
	sort.Strings(sorted)
	for i := range a {
		if a[i] != sorted[i] {
			return false
		}
	}
	return true
}
