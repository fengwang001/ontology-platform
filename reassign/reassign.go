package reassign

// completeLocked 按目标列表完成重分配：
// 副本列表改为 target 次序；ISR 取与 target 的交集（保持 target 次序）；
// 领导者不在 target 内时取 target 中第一个在 ISR 的成员，否则不变。
func (c *Controller) completeLocked(p *partition) {
	target := p.target
	newISR := make(map[string]bool, len(target))
	for _, node := range target {
		if p.isr[node] {
			newISR[node] = true
		}
	}
	p.replicas = append([]string(nil), target...)
	p.isr = newISR
	if p.leader != "" && newISR[p.leader] {
		// 现领导者仍在目标 ISR 中，保持不变。
	} else {
		p.leader = firstInISR(newISR, target)
	}
	p.running = false
	p.orig = nil
	p.target = nil
	c.runningCount--
}

// StartReassign 开始把 partition 的副本集合迁移到 target。
// 拒绝原因按以下次序判定：分区不存在、已有进行中、空目标、目标含重复、
// 含未知节点、含宕机节点、与当前副本列表完全相同、进行中数已达上限。
func (c *Controller) StartReassign(partition string, target []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	p, ok := c.partitions[partition]
	if !ok {
		c.logf("StartReassign partition=%q target=%v -> reject %s: partition not found", partition, target, ReasonPartitionNotFound)
		return fail(ReasonPartitionNotFound)
	}
	if p.running {
		c.logf("StartReassign partition=%q target=%v -> reject %s: reassignment already running (orig=%v target=%v)",
			partition, target, ReasonAlreadyRunning, p.orig, p.target)
		return fail(ReasonAlreadyRunning)
	}
	if len(target) == 0 {
		c.logf("StartReassign partition=%q target=%v -> reject %s: empty target", partition, target, ReasonEmptyTarget)
		return fail(ReasonEmptyTarget)
	}
	seen := make(map[string]bool, len(target))
	for _, node := range target {
		if seen[node] {
			c.logf("StartReassign partition=%q target=%v -> reject %s: duplicate node %q",
				partition, target, ReasonDuplicateReplica, node)
			return fail(ReasonDuplicateReplica)
		}
		seen[node] = true
	}
	for _, node := range target {
		if _, known := c.nodes[node]; !known {
			c.logf("StartReassign partition=%q target=%v -> reject %s: unknown node %q",
				partition, target, ReasonUnknownNode, node)
			return fail(ReasonUnknownNode)
		}
	}
	for _, node := range target {
		if !c.nodes[node] {
			c.logf("StartReassign partition=%q target=%v -> reject %s: node %q is down",
				partition, target, ReasonNodeDown, node)
			return fail(ReasonNodeDown)
		}
	}
	if equalOrdered(p.replicas, target) {
		c.logf("StartReassign partition=%q target=%v -> reject %s: target identical to current replicas",
			partition, target, ReasonTargetUnchanged)
		return fail(ReasonTargetUnchanged)
	}

	// 仅次序不同（成员完全相同）：开始即完成，领导者不变。
	if sameMembers(p.replicas, target) {
		prev := p.view()
		p.replicas = append([]string(nil), target...)
		c.logf("StartReassign partition=%q target=%v -> reorder-only, completed immediately (leader unchanged); before=%s after=%s",
			partition, target, viewString(prev), viewString(p.view()))
		return nil
	}

	if c.runningCount >= c.maxConcurrent {
		c.logf("StartReassign partition=%q target=%v -> reject %s: running=%d limit=%d",
			partition, target, ReasonConcurrencyLimit, c.runningCount, c.maxConcurrent)
		return fail(ReasonConcurrencyLimit)
	}

	orig := append([]string(nil), p.replicas...)
	// 副本列表 = 原列表 + target 中新增节点（按 target 次序）。
	merged := append([]string(nil), orig...)
	for _, node := range target {
		if !contains(orig, node) {
			merged = append(merged, node)
		}
	}
	p.running = true
	p.orig = orig
	p.target = append([]string(nil), target...)
	p.replicas = merged
	c.runningCount++

	c.logf("StartReassign partition=%q target=%v -> started: %s; running=%d/%d",
		partition, target, viewString(p.view()), c.runningCount, c.maxConcurrent)
	return nil
}

// CatchUp 上报 node 已追平：存活且在副本列表但不在 ISR 的副本加入 ISR；
// 分区无领导者时首个加入者成为领导者。随后若目标成员全在 ISR 则完成。
func (c *Controller) CatchUp(partition, node string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	p, ok := c.partitions[partition]
	if !ok {
		c.logf("CatchUp partition=%q node=%q -> reject %s: partition not found", partition, node, ReasonPartitionNotFound)
		return fail(ReasonPartitionNotFound)
	}
	if !contains(p.replicas, node) {
		c.logf("CatchUp partition=%q node=%q -> reject %s: node not in replicas %v",
			partition, node, ReasonNodeNotReplica, p.replicas)
		return fail(ReasonNodeNotReplica)
	}
	if !c.nodes[node] {
		c.logf("CatchUp partition=%q node=%q -> reject %s: node is down", partition, node, ReasonNodeDown)
		return fail(ReasonNodeDown)
	}
	if p.isr[node] {
		c.logf("CatchUp partition=%q node=%q -> reject %s: node already in ISR %v",
			partition, node, ReasonAlreadyInISR, p.isrOrdered())
		return fail(ReasonAlreadyInISR)
	}

	p.isr[node] = true
	becameLeader := false
	if p.leader == "" {
		p.leader = node
		becameLeader = true
	}

	completed := false
	target := []string(nil)
	if p.running {
		if allInISR(p.isr, p.target) {
			target = append([]string(nil), p.target...)
			c.completeLocked(p)
			completed = true
		}
	}

	c.logf("CatchUp partition=%q node=%q -> joined ISR (became_leader=%t, auto_completed=%t): %s",
		partition, node, becameLeader, completed, viewString(p.view()))
	if completed {
		c.logf("CatchUp partition=%q: completion decided because all target members %v are in ISR",
			partition, target)
	}
	return nil
}

// Cancel 取消进行中的重分配：副本列表恢复为原列表；ISR 取与原列表的交集；
// 领导者不在其中时取原列表中第一个在 ISR 的成员，无则无领导者。
func (c *Controller) Cancel(partition string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	p, ok := c.partitions[partition]
	if !ok {
		c.logf("Cancel partition=%q -> reject %s: partition not found", partition, ReasonPartitionNotFound)
		return fail(ReasonPartitionNotFound)
	}
	if !p.running {
		c.logf("Cancel partition=%q -> reject %s: no reassignment in progress", partition, ReasonNotRunning)
		return fail(ReasonNotRunning)
	}

	prev := p.view()
	orig := p.orig
	rolledISR := make(map[string]bool, len(orig))
	for _, node := range orig {
		if p.isr[node] {
			rolledISR[node] = true
		}
	}
	p.replicas = append([]string(nil), orig...)
	p.isr = rolledISR
	if p.leader != "" && rolledISR[p.leader] {
		// 现领导者仍在原列表 ISR 中，保持不变。
	} else {
		p.leader = firstInISR(rolledISR, orig)
	}
	p.running = false
	p.orig = nil
	p.target = nil
	c.runningCount--

	c.logf("Cancel partition=%q -> rolled back; before=%s after=%s; decision: leader=%q (first in-ISR node of orig order, none => no leader)",
		partition, viewString(prev), viewString(p.view()), p.leader)
	return nil
}

func equalOrdered(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func allInISR(isr map[string]bool, nodes []string) bool {
	for _, node := range nodes {
		if !isr[node] {
			return false
		}
	}
	return true
}
