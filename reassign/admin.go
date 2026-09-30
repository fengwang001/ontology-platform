package reassign

// AddNode 注册一个节点，默认存活。重复注册返回错误。
func (c *Controller) AddNode(node string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.nodes[node] {
		c.logf("AddNode input=%q -> rejected: node already exists", node)
		return errNodeExists
	}
	c.nodes[node] = true
	c.logf("AddNode input=%q -> registered alive", node)
	return nil
}

// AddPartition 创建分区。初始 ISR 为全部存活的初始副本（保持副本列表次序），
// 领导者取 ISR 首个成员。副本列表为空、含重复或未知节点时拒绝。
func (c *Controller) AddPartition(name string, replicas []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.partitions[name]; exists {
		c.logf("AddPartition input=%q replicas=%v -> rejected: partition already exists", name, replicas)
		return errPartitionExists
	}
	if len(replicas) == 0 {
		c.logf("AddPartition input=%q -> rejected: empty replicas", name)
		return errEmptyReplicas
	}
	seen := make(map[string]bool, len(replicas))
	for _, node := range replicas {
		if seen[node] {
			c.logf("AddPartition input=%q replicas=%v -> rejected: duplicate node %q", name, replicas, node)
			return errDuplicateNode
		}
		seen[node] = true
		alive, known := c.nodes[node]
		if !known {
			c.logf("AddPartition input=%q replicas=%v -> rejected: unknown node %q", name, replicas, node)
			return errNodeUnknown
		}
		if !alive {
			c.logf("AddPartition input=%q replicas=%v -> note: node %q is down, starts outside ISR", name, replicas, node)
		}
	}

	p := &partition{
		replicas: append([]string(nil), replicas...),
		isr:      make(map[string]bool),
	}
	for _, node := range replicas {
		if c.nodes[node] {
			p.isr[node] = true
		}
	}
	p.leader = firstInISR(p.isr, p.replicas)
	c.partitions[name] = p

	c.logf("AddPartition input=%q replicas=%v -> created: %s", name, replicas, viewString(p.view()))
	return nil
}

// Get 查询单个分区快照。分区不存在时 ok 为 false。
func (c *Controller) Get(partition string) (view PartitionView, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, exists := c.partitions[partition]
	if !exists {
		c.logf("Get input=%q -> not found", partition)
		return PartitionView{}, false
	}
	view = p.view()
	c.logf("Get input=%q -> %s", partition, viewString(view))
	return view, true
}

// RunningCount 返回当前进行中的重分配数量。
func (c *Controller) RunningCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.runningCount
}
