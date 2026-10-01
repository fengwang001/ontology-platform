package reassign

// AddNode 注册一个已知节点，初始为存活。
func (c *Controller) AddNode(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nodes[id] = true
}

// CreatePartition 创建分区；副本列表即初始 ISR，领导者为第一个副本。
func (c *Controller) CreatePartition(id string, replicas []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := append([]string(nil), replicas...)
	isr := make(map[string]bool, len(replicas))
	leader := ""
	for _, r := range replicas {
		isr[r] = true
		if leader == "" {
			leader = r
		}
	}
	c.partitions[id] = &partition{replicas: cp, isr: isr, leader: leader}
}

// GetPartition 返回分区状态快照；第二个返回值表示分区是否存在。
func (c *Controller) GetPartition(id string) (PartitionState, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.partitions[id]
	if !ok {
		return PartitionState{}, false
	}
	return p.snapshot(), true
}

func (p *partition) snapshot() PartitionState {
	st := PartitionState{
		Replicas:    append([]string(nil), p.replicas...),
		Leader:      p.leader,
		Reassigning: p.original != nil,
	}
	for _, r := range p.replicas {
		if p.isr[r] {
			st.ISR = append(st.ISR, r)
		}
	}
	if p.original != nil {
		st.Target = append([]string(nil), p.target...)
	}
	return st
}

// StartReassign 开始把分区 id 的副本集合迁移到 target。
// 校验次序：分区不存在、已有进行中、目标非法、目标含宕机节点、
// 与当前列表完全相同、并发上限。
func (c *Controller) StartReassign(id string, target []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.partitions[id]
	if !ok {
		return ErrPartitionNotFound
	}
	if p.original != nil {
		return ErrReassignInProgress
	}
	if err := c.validateTarget(target); err != nil {
		return err
	}
	for _, t := range target {
		if !c.nodes[t] {
			return ErrTargetNodeDown
		}
	}
	if equalStrings(p.replicas, target) {
		return ErrTargetUnchanged
	}
	if c.inflight >= c.maxInflight {
		return ErrConcurrencyLimit
	}
	p.original = append([]string(nil), p.replicas...)
	p.target = append([]string(nil), target...)
	inCur := make(map[string]bool, len(p.replicas))
	for _, r := range p.replicas {
		inCur[r] = true
	}
	for _, t := range target {
		if !inCur[t] {
			p.replicas = append(p.replicas, t)
		}
	}
	c.inflight++
	c.maybeComplete(p)
	return nil
}

func (c *Controller) validateTarget(target []string) error {
	if len(target) == 0 {
		return ErrInvalidTarget
	}
	seen := make(map[string]bool, len(target))
	for _, t := range target {
		if seen[t] {
			return ErrInvalidTarget
		}
		seen[t] = true
		if _, ok := c.nodes[t]; !ok {
			return ErrInvalidTarget
		}
	}
	return nil
}

// maybeComplete 在目标成员全部进入 ISR 时完成重分配；调用方须持有锁。
func (c *Controller) maybeComplete(p *partition) {
	if p.original == nil {
		return
	}
	for _, t := range p.target {
		if !p.isr[t] {
			return
		}
	}
	if !contains(p.target, p.leader) {
		p.leader = firstInISR(p.target, p.isr)
	}
	p.replicas = append([]string(nil), p.target...)
	for r := range p.isr {
		if !contains(p.target, r) {
			delete(p.isr, r)
		}
	}
	p.original = nil
	p.target = nil
	c.inflight--
}

// ReportCaughtUp 上报节点 node 在分区 id 上已追平，将其加入 ISR。
// 校验次序：分区不存在、节点不在副本列表、节点宕机、已在 ISR。
func (c *Controller) ReportCaughtUp(id, node string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.partitions[id]
	if !ok {
		return ErrPartitionNotFound
	}
	if !contains(p.replicas, node) {
		return ErrNotInReplicas
	}
	if !c.nodes[node] {
		return ErrNodeDown
	}
	if p.isr[node] {
		return ErrAlreadyInISR
	}
	p.isr[node] = true
	if p.leader == "" {
		p.leader = node
	}
	c.maybeComplete(p)
	return nil
}

// CancelReassign 取消分区 id 上进行中的重分配，回滚到原副本列表。
func (c *Controller) CancelReassign(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.partitions[id]
	if !ok {
		return ErrPartitionNotFound
	}
	if p.original == nil {
		return ErrNoReassign
	}
	p.replicas = p.original
	for r := range p.isr {
		if !contains(p.original, r) {
			delete(p.isr, r)
		}
	}
	if !contains(p.original, p.leader) {
		p.leader = firstInISR(p.original, p.isr)
	}
	p.original = nil
	p.target = nil
	c.inflight--
	return nil
}

// NodeDown 标记节点宕机：其退出所有分区的 ISR，
// 若为领导者则改选副本列表中第一个仍在 ISR 的成员。
func (c *Controller) NodeDown(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.nodes[id]; !ok {
		return
	}
	c.nodes[id] = false
	for _, p := range c.partitions {
		delete(p.isr, id)
		if p.leader == id {
			p.leader = firstInISR(p.replicas, p.isr)
		}
	}
}

// NodeUp 标记节点恢复存活；不自动回到 ISR，须再次追平上报。
func (c *Controller) NodeUp(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.nodes[id]; ok {
		c.nodes[id] = true
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func firstInISR(list []string, isr map[string]bool) string {
	for _, r := range list {
		if isr[r] {
			return r
		}
	}
	return ""
}

func equalStrings(a, b []string) bool {
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
