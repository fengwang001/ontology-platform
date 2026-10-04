package core

import "sync"

const (
	Owner  = 3
	Admin  = 2
	Member = 1
)

type qnode struct {
	prev, next *qnode
	user       int64
}

type muteRec struct {
	until int64
	level int
}

type Room struct {
	mu      sync.Mutex
	micCnt  int
	slots   []int64
	roles   map[int64]int
	mutes   map[int64]muteRec
	head    *qnode
	tail    *qnode
	qnodeOf map[int64]*qnode
	maxNow  int64
	touched int // 队列节点显式触碰计数，每操作开始重置
	scanned int // 最近一次补麦扫描的队列项数
}

// New 创建含 M 个麦位的房间；M 越界视为编程错误直接 panic。
func New(m int) *Room {
	if m < 1 || m > 64 {
		panic("mic count out of range 1..64")
	}
	r := &Room{
		micCnt:  m,
		slots:   make([]int64, m),
		roles:   make(map[int64]int),
		mutes:   make(map[int64]muteRec),
		qnodeOf: make(map[int64]*qnode),
	}
	r.head = &qnode{}
	r.tail = &qnode{}
	r.head.next = r.tail
	r.tail.prev = r.head
	return r
}

func (r *Room) present(u int64) bool { _, ok := r.roles[u]; return ok }

// muted 报告 u 在 now 时刻是否有生效禁言。到期记录视作不存在。
func (r *Room) muted(u int64, now int64) bool {
	m, ok := r.mutes[u]
	return ok && now < m.until
}

func (r *Room) firstFreeSlot() int {
	for i, u := range r.slots {
		if u == 0 {
			return i
		}
	}
	return -1
}

func (r *Room) enqueue(u int64) {
	r.touched++ // 新节点自身
	r.touched++ // 哨兵 tail
	n := &qnode{user: u, prev: r.tail.prev, next: r.tail}
	r.tail.prev.next = n
	r.tail.prev = n
	r.qnodeOf[u] = n
}

// removeNode 摘除一个链表节点，触碰前驱、自身、后继共至多 3 个节点。
func (r *Room) removeNode(n *qnode) {
	r.touched++ // n.prev
	r.touched++ // n
	r.touched++ // n.next
	n.prev.next = n.next
	n.next.prev = n.prev
	n.prev, n.next = nil, nil
}

func (r *Room) inQueue(u int64) bool { _, ok := r.qnodeOf[u]; return ok }

func (r *Room) onMic(u int64) bool {
	for _, s := range r.slots {
		if s == u {
			return true
		}
	}
	return false
}

func (r *Room) leaveMicOrQueue(u int64) {
	if n, ok := r.qnodeOf[u]; ok {
		r.removeNode(n)
		delete(r.qnodeOf, u)
		return
	}
	for i, s := range r.slots {
		if s == u {
			r.slots[i] = 0
			return
		}
	}
}

// kickOffMic 仅让在麦用户下麦；队列中的用户保留原位。
func (r *Room) kickOffMic(u int64) {
	for i, s := range r.slots {
		if s == u {
			r.slots[i] = 0
			return
		}
	}
}

// fill 执行一次补麦：有空麦时从队首单次扫描，未禁言者上最小编号空麦，
// 禁言者跳过留位；空麦填满或扫到队尾即停。
func (r *Room) fill(now int64) {
	r.scanned = 0
	n := r.head.next
	for n != r.tail {
		r.scanned++
		slot := r.firstFreeSlot()
		if slot < 0 {
			return
		}
		u := n.user
		nxt := n.next
		if r.muted(u, now) {
			n = nxt
			continue
		}
		r.removeNode(n)
		delete(r.qnodeOf, u)
		r.slots[slot] = u
		n = nxt
	}
}

type snapshot struct {
	slots   []int64
	roles   map[int64]int
	mutes   map[int64]muteRec
	queue   []int64
	maxNow  int64
	touched int
}

func (r *Room) save() snapshot {
	s := snapshot{
		slots:   append([]int64(nil), r.slots...),
		roles:   make(map[int64]int, len(r.roles)),
		mutes:   make(map[int64]muteRec, len(r.mutes)),
		maxNow:  r.maxNow,
		touched: r.touched,
	}
	for u, lv := range r.roles {
		s.roles[u] = lv
	}
	for u, m := range r.mutes {
		s.mutes[u] = m
	}
	for n := r.head.next; n != r.tail; n = n.next {
		s.queue = append(s.queue, n.user)
	}
	return s
}

func (r *Room) restore(s snapshot) {
	r.slots = append([]int64(nil), s.slots...)
	r.roles = make(map[int64]int, len(s.roles))
	for u, lv := range s.roles {
		r.roles[u] = lv
	}
	r.mutes = make(map[int64]muteRec, len(s.mutes))
	for u, m := range s.mutes {
		r.mutes[u] = m
	}
	r.head = &qnode{}
	r.tail = &qnode{}
	r.head.next = r.tail
	r.tail.prev = r.head
	r.qnodeOf = make(map[int64]*qnode, len(s.queue))
	for _, u := range s.queue {
		r.enqueue(u)
	}
	r.maxNow = s.maxNow
	// 恢复期间重建链表的触碰不算业务触碰，整体回到快照时刻。
	r.touched = s.touched
}
