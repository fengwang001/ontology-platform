package reassign

import "sort"

// NodeDown 将节点标记为宕机：退出所有分区的 ISR；若为某分区领导者，
// 该分区改选副本列表中第一个仍在 ISR 的成员，无则无领导者。
// 宕机不改变副本列表，也不自动完成或取消进行中的重分配。
func (c *Controller) NodeDown(node string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if alive, known := c.nodes[node]; known && !alive {
		c.logf("NodeDown input=%q -> no-op: node already down", node)
		return
	}
	c.nodes[node] = false

	affected := make([]string, 0)
	names := make([]string, 0, len(c.partitions))
	for name := range c.partitions {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p := c.partitions[name]
		if !contains(p.replicas, node) {
			continue
		}
		wasInISR := p.isr[node]
		wasLeader := p.leader == node
		delete(p.isr, node)
		if wasLeader {
			p.leader = firstInISR(p.isr, p.replicas)
		}
		if wasInISR || wasLeader {
			affected = append(affected, name)
		}
		c.logf("NodeDown input=%q partition=%q: removed_from_isr=%t was_leader=%t new_leader=%q -> %s",
			node, name, wasInISR, wasLeader, p.leader, viewString(p.view()))
	}
	c.logf("NodeDown input=%q -> marked down; affected partitions=%v", node, affected)
}

// NodeUp 将节点标记为存活，不自动加入任何 ISR，需再次追平上报。
func (c *Controller) NodeUp(node string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if alive, known := c.nodes[node]; known && alive {
		c.logf("NodeUp input=%q -> no-op: node already alive", node)
		return
	}
	c.nodes[node] = true
	c.logf("NodeUp input=%q -> marked alive; ISR memberships unchanged (needs catch-up reports)", node)
}
