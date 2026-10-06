package taxipool

import "time"

// qnode 是链表节点。
type qnode struct {
	entry qentry
	prev  *qnode
	next  *qnode
}

// qentry 是队列中的一个司机。
type qentry struct {
	driver   string
	enqAt    time.Time
	priority bool
}

// terminalQueue 是单条候机楼队列：链表维护次序，map 维护节点索引。
type terminalQueue struct {
	id       string
	capacity int
	head     *qnode // 哨兵，head.next 为队首
	tail     *qnode // 哨兵，tail.prev 为队尾
	index    map[string]*qnode
	priority int
	size     int
	// lastPositionScan：最近一次位置查询实际遍历的节点数，用于可验证地证明
	// 查询开销只依赖当前队列长度而非历史入池总数。
	lastPositionScan int
}

func newTerminalQueue(id string, capacity int) *terminalQueue {
	head, tail := new(qnode), new(qnode)
	head.next = tail
	tail.prev = head
	return &terminalQueue{
		id:       id,
		capacity: capacity,
		head:     head,
		tail:     tail,
		index:    make(map[string]*qnode),
	}
}

func (q *terminalQueue) len() int { return q.size }

func (q *terminalQueue) full() bool { return q.size >= q.capacity }

func (q *terminalQueue) contains(driver string) bool { _, ok := q.index[driver]; return ok }

// before 定义同一优先级区段内的次序：入池时刻更早者在前；并列时司机标识字典序更小者在前。
func before(a, b qentry) bool {
	if !a.enqAt.Equal(b.enqAt) {
		return a.enqAt.Before(b.enqAt)
	}
	return a.driver < b.driver
}

// insertAfter 把节点插在 at 之后。
func (q *terminalQueue) insertAfter(at, n *qnode) {
	n.prev = at
	n.next = at.next
	at.next.prev = n
	at.next = n
}

// enqueue 按规则入队，返回入队后的前方人数。
func (q *terminalQueue) enqueue(driver string, at time.Time, priority bool) int {
	n := &qnode{entry: qentry{driver: driver, enqAt: at, priority: priority}}
	var pos int
	if priority {
		// 排在所有普通司机之前、已有优先司机之后：即优先区段末尾。
		cur := q.tail.prev
		for cur != q.head && !cur.entry.priority {
			cur = cur.prev
		}
		// 同优先级内再按（时刻、标识）排序：继续前移直到遇到严格更早者。
		for cur != q.head && cur.entry.priority && before(n.entry, cur.entry) {
			cur = cur.prev
		}
		q.insertAfter(cur, n)
		// 位置 = 其前方节点数。
		for p := n.prev; p != q.head; p = p.prev {
			pos++
		}
		q.priority++
	} else {
		// 普通区段：在普通司机中按（时刻、标识）插入。
		cur := q.tail.prev
		for cur != q.head && cur.entry.priority {
			cur = cur.prev
		}
		for cur != q.head && before(n.entry, cur.entry) {
			cur = cur.prev
		}
		q.insertAfter(cur, n)
		for p := n.prev; p != q.head; p = p.prev {
			pos++
		}
	}
	q.index[driver] = n
	q.size++
	return pos
}

// remove 删除队列中的司机；司机不在队列中返回 false。
func (q *terminalQueue) remove(driver string) bool {
	n, ok := q.index[driver]
	if !ok {
		return false
	}
	n.prev.next = n.next
	n.next.prev = n.prev
	if n.entry.priority {
		q.priority--
	}
	delete(q.index, driver)
	q.size--
	return true
}

// popFront 取走队首；空队列返回零值与 false。
func (q *terminalQueue) popFront() (qentry, bool) {
	if q.size == 0 {
		return qentry{}, false
	}
	n := q.head.next
	e := n.entry
	q.head.next = n.next
	n.next.prev = q.head
	delete(q.index, e.driver)
	if e.priority {
		q.priority--
	}
	q.size--
	return e, true
}

// front 返回队首（不删除）。
func (q *terminalQueue) front() (qentry, bool) {
	if q.size == 0 {
		return qentry{}, false
	}
	return q.head.next.entry, true
}

func (q *terminalQueue) priorityCount() int { return q.priority }

// position 返回司机前方人数；从队首顺序遍历到该节点，步数记录到 lastPositionScan。
// 司机不在队列中返回 0,false。遍历节点数 <= 当前队列长度，与历史入池总数无关。
func (q *terminalQueue) position(driver string) (int, bool) {
	q.lastPositionScan = 0
	pos := 0
	for cur := q.head.next; cur != q.tail; cur = cur.next {
		q.lastPositionScan++
		if cur.entry.driver == driver {
			return pos, true
		}
		pos++
	}
	return 0, false
}

// LastPositionScan 返回最近一次 position 查询遍历的节点数（验证用）。
func (q *terminalQueue) LastPositionScan() int { return q.lastPositionScan }

// snapshot 按队首到队尾返回司机与优先标记（测试/朴素对照用）。
func (q *terminalQueue) snapshot() []qentry {
	out := make([]qentry, 0, q.size)
	for cur := q.head.next; cur != q.tail; cur = cur.next {
		out = append(out, cur.entry)
	}
	return out
}
