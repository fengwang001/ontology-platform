package room

// member 是按加入次序组织的在室玩家链表节点。
type member struct {
	user string
	prev *member
	next *member
}

// registry 维护在室玩家集合与就绪计数。
// 所有操作 O(1)，且不随历史事件数增长。
type registry struct {
	head  *member
	tail  *member
	nodes map[string]*member

	ready    map[string]bool
	notReady int
}

func newRegistry() *registry {
	return &registry{
		nodes: map[string]*member{},
		ready: map[string]bool{},
	}
}

// add 追加一名未就绪玩家。调用方须保证其当前不在室。
func (g *registry) add(user string) {
	m := &member{user: user, prev: g.tail}
	g.nodes[user] = m
	if g.tail != nil {
		g.tail.next = m
	} else {
		g.head = m
	}
	g.tail = m
	g.notReady++
}

// remove 摘除玩家并清理其就绪状态。摘除的节点立即从链表与映射中消失，
// 因此结构规模只取决于当前在室人数，与历史加入/离开次数无关。
func (g *registry) remove(user string) {
	m := g.nodes[user]
	if m == nil {
		return
	}
	if m.prev != nil {
		m.prev.next = m.next
	} else {
		g.head = m.next
	}
	if m.next != nil {
		m.next.prev = m.prev
	} else {
		g.tail = m.prev
	}
	delete(g.nodes, user)
	if g.ready[user] {
		delete(g.ready, user)
	} else {
		g.notReady--
	}
}

func (g *registry) has(user string) bool {
	_, ok := g.nodes[user]
	return ok
}

// owner 返回当前最早加入的在室玩家（链表头），无人时返回空串。
func (g *registry) owner() string {
	if g.head == nil {
		return ""
	}
	return g.head.user
}

func (g *registry) size() int { return len(g.nodes) }

// setReady 切换就绪标记。返回切换后是否就绪；若标记未发生变化则第二个
// 返回值为 false（用于裁决“已就绪再就绪/已取消再取消”的状态冲突）。
func (g *registry) setReady(user string, on bool) (ready bool, changed bool) {
	if g.ready[user] == on {
		return on, false
	}
	g.ready[user] = on
	if on {
		g.notReady--
	} else {
		g.notReady++
	}
	return on, true
}

// allReady 以未就绪计数器判断，O(1)，不随在室人数线性增长。
func (g *registry) allReady() bool { return g.notReady == 0 }

// present 返回按加入次序排列的在室玩家（快照用，仅测试/查询使用）。
func (g *registry) present() []string {
	out := make([]string, 0, len(g.nodes))
	for m := g.head; m != nil; m = m.next {
		out = append(out, m.user)
	}
	return out
}
