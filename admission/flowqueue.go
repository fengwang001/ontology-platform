package admission

import "sort"

// Time 为调用方注入的逻辑时刻（单调递增，不回退）。
type Time int64

// waiter 是一个排队请求。
type waiter struct {
	id       string
	seats    int64
	enqueued Time
	deadline Time
	flow     string
	level    string
	seq      int64
	prev     *waiter
	next     *waiter
}

// flowNode 是一个非空流的 FIFO 队列，也是按最近“空→非空”时间维护的线性链表节点。
type flowNode struct {
	name        string
	first       *waiter
	last        *waiter
	prev        *flowNode
	next        *flowNode
	activatedAt int64
}

// flowQueues 始终只包含非空流；head→tail 是最近一次空→非空的顺序。
// 每次 pump 从 head 扫描到 tail；成功服务一个流后把该流移到 tail，
// 从而同一个流的下一个 FIFO 请求自然排在所有尚未服务流之后。
// 队首放不下时直接停止；head 仍指向该流，下一次 pump 先重试它。
type flowQueues struct {
	nodes map[string]*flowNode
	head  *flowNode
	tail  *flowNode
	seq   int64
}

func newFlowQueues() *flowQueues                                { return &flowQueues{nodes: map[string]*flowNode{}} }
func (fq *flowQueues) empty() bool                              { return fq.head == nil }
func (fq *flowQueues) flowNode(flow string) *flowNode           { return fq.nodes[flow] }
func (fq *flowQueues) peekHead() *flowNode                      { return fq.head }
func (fq *flowQueues) lastServedNode() string                   { return "" }
func (fq *flowQueues) lastServedHeadID() string                 { return "" }
func (fq *flowQueues) resetWindowAfterExpiry(lastServed string) {}
func (fq *flowQueues) resetWindow()                             {}
func (fq *flowQueues) markQueueDirty()                          {}

// activateAppend 在流从空变非空时追加到尾部；非空流追加等待者时顺序不变。
func (fq *flowQueues) activateAppend(flow string) *flowNode {
	if node := fq.nodes[flow]; node != nil {
		return node
	}
	fq.seq++
	node := &flowNode{name: flow, activatedAt: fq.seq}
	fq.nodes[flow] = node
	if fq.head == nil {
		fq.head = node
		fq.tail = node
		return node
	}
	node.prev = fq.tail
	fq.tail.next = node
	fq.tail = node
	return node
}

func (fq *flowQueues) enqueueWaiter(node *flowNode, w *waiter) {
	if node.first == nil {
		node.first, node.last = w, w
		return
	}
	w.prev = node.last
	node.last.next = w
	node.last = w
}

// removeHead 移除流队首；若流仍非空，把它移到激活顺序尾部；若空则摘链。
func (fq *flowQueues) removeHead(node *flowNode) *waiter {
	w := node.first
	if w == nil {
		return nil
	}
	if w.next != nil {
		node.first = w.next
		w.next.prev = nil
		fq.moveToTail(node)
	} else {
		node.first, node.last = nil, nil
		fq.detach(node)
	}
	w.prev, w.next = nil, nil
	return w
}

func (fq *flowQueues) removeWaiter(node *flowNode, w *waiter) {
	if node.first == w {
		fq.removeHead(node)
		return
	}
	if w.prev != nil {
		w.prev.next = w.next
	}
	if w.next != nil {
		w.next.prev = w.prev
	} else {
		node.last = w.prev
	}
	if node.first == nil {
		fq.detach(node)
	}
	w.prev, w.next = nil, nil
}

func (fq *flowQueues) detach(node *flowNode) {
	if fq.nodes[node.name] != node {
		return
	}
	delete(fq.nodes, node.name)
	prev, next := node.prev, node.next
	if prev != nil {
		prev.next = next
	}
	if next != nil {
		next.prev = prev
	}
	if fq.head == node {
		fq.head = next
	}
	if fq.tail == node {
		fq.tail = prev
	}
	node.prev, node.next = nil, nil
}

func (fq *flowQueues) moveToTail(node *flowNode) {
	if fq.tail == node {
		return
	}
	prev, next := node.prev, node.next
	if prev != nil {
		prev.next = next
	}
	if next != nil {
		next.prev = prev
	}
	if fq.head == node {
		fq.head = next
	}
	node.prev = fq.tail
	node.next = nil
	if fq.tail != nil {
		fq.tail.next = node
	}
	fq.tail = node
	if fq.head == nil {
		fq.head = node
	}
	fq.seq++
	node.activatedAt = fq.seq
}

func (fq *flowQueues) nextCandidate() *flowNode         { return fq.head }
func (fq *flowQueues) markServed(node *flowNode)        {}
func (fq *flowQueues) stopAt(node *flowNode, w *waiter) {}
func (fq *flowQueues) isBlocked() bool                  { return false }
func (fq *flowQueues) pumpEnded(blocked bool)           {}

// rebuildActivationOrder 仅热更新使用：按最近激活时间重建链表。
func (fq *flowQueues) rebuildActivationOrder() {
	var nodes []*flowNode
	for _, n := range fq.nodes {
		if n.first != nil {
			nodes = append(nodes, n)
		} else {
			delete(fq.nodes, n.name)
		}
	}
	sort.SliceStable(nodes, func(i, j int) bool {
		if nodes[i].activatedAt != nodes[j].activatedAt {
			return nodes[i].activatedAt < nodes[j].activatedAt
		}
		return nodes[i].name < nodes[j].name
	})
	for i, n := range nodes {
		if i == 0 {
			n.prev = nil
		} else {
			n.prev = nodes[i-1]
		}
		if i+1 == len(nodes) {
			n.next = nil
		} else {
			n.next = nodes[i+1]
		}
	}
	if len(nodes) == 0 {
		fq.head, fq.tail = nil, nil
	} else {
		fq.head, fq.tail = nodes[0], nodes[len(nodes)-1]
	}
}

func (fq *flowQueues) forEachWaiter(fn func(w *waiter)) {
	for node := fq.head; node != nil; node = node.next {
		for w := node.first; w != nil; w = w.next {
			fn(w)
		}
	}
}
