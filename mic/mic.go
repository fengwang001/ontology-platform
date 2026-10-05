// Package mic 维护麦位与排麦队列。
//
// 队列为自制双向链表：从队列中部移除一人只触碰自身与前驱、后继
// 共 3 个节点（touched 计数器），与队列长度无关。补麦为单指针
// 一趟扫描，检查的队列项数不超过上麦人数加被跳过的禁言者数加 1
// （checked 计数器）。
package mic

type node struct {
	user string
	prev *node
	next *node
}

// State 为麦序状态：slots 为麦位（"" 表示空麦），链表为排麦队列。
type State struct {
	slots   []string
	onMic   map[string]int
	head    *node
	tail    *node
	pos     map[string]*node
	qLen    int
	touched int // 链表移除触碰的节点数
	checked int // 补麦扫描检查的队列项数
}

// New 创建 M 个麦位的麦序状态，M 取 1 到 64。
func New(m int) *State {
	return &State{
		slots: make([]string, m),
		onMic: make(map[string]int),
		pos:   make(map[string]*node),
	}
}

// Clone 深拷贝整个状态（含计数器），供拒绝回滚使用。
func (s *State) Clone() *State {
	c := &State{
		slots:   make([]string, len(s.slots)),
		onMic:   make(map[string]int, len(s.onMic)),
		pos:     make(map[string]*node, len(s.pos)),
		qLen:    s.qLen,
		touched: s.touched,
		checked: s.checked,
	}
	copy(c.slots, s.slots)
	for u, i := range s.onMic {
		c.onMic[u] = i
	}
	var prev *node
	for n := s.head; n != nil; n = n.next {
		m := &node{user: n.user, prev: prev}
		if prev == nil {
			c.head = m
		} else {
			prev.next = m
		}
		prev = m
		c.pos[m.user] = m
	}
	c.tail = prev
	return c
}

func (s *State) OnMic(u string) bool { _, ok := s.onMic[u]; return ok }

func (s *State) InQueue(u string) bool { _, ok := s.pos[u]; return ok }

// Queue 返回队首到队尾的用户序列。
func (s *State) Queue() []string {
	out := make([]string, 0, s.qLen)
	for n := s.head; n != nil; n = n.next {
		out = append(out, n.user)
	}
	return out
}

// Slots 返回麦位副本。
func (s *State) Slots() []string {
	out := make([]string, len(s.slots))
	copy(out, s.slots)
	return out
}

func (s *State) lowestEmpty() int {
	for i, u := range s.slots {
		if u == "" {
			return i
		}
	}
	return -1
}

// Take 上麦：有空麦则占编号最小的空麦，否则排到队尾。
// 调用方保证用户既不在麦上也不在队列中。
func (s *State) Take(u string) {
	if i := s.lowestEmpty(); i >= 0 {
		s.slots[i] = u
		s.onMic[u] = i
		return
	}
	n := &node{user: u, prev: s.tail}
	if s.tail == nil {
		s.head = n
	} else {
		s.tail.next = n
	}
	s.tail = n
	s.pos[u] = n
	s.qLen++
}

// remove 摘除链表节点，只触碰自身与前驱、后继。
func (s *State) remove(n *node) {
	s.touched++
	if n.prev != nil {
		s.touched++
		n.prev.next = n.next
	} else {
		s.head = n.next
	}
	if n.next != nil {
		s.touched++
		n.next.prev = n.prev
	} else {
		s.tail = n.prev
	}
	delete(s.pos, n.user)
	s.qLen--
}

// Drop 下麦或出队；两者都不是时无操作（由调用方先行判定）。
func (s *State) Drop(u string) {
	if i, ok := s.onMic[u]; ok {
		s.slots[i] = ""
		delete(s.onMic, u)
		return
	}
	if n, ok := s.pos[u]; ok {
		s.remove(n)
	}
}

// ForceDropMic 仅在麦时强制下麦（不入队），用于禁言与离开。
func (s *State) ForceDropMic(u string) {
	if i, ok := s.onMic[u]; ok {
		s.slots[i] = ""
		delete(s.onMic, u)
	}
}

// Fill 补麦：只要有空麦，从队首向后找第一个未禁言者出队上最小空麦，
// 被跳过的禁言者留在原位；单指针一趟扫描直到没有空麦或没有合格者。
func (s *State) Fill(muted func(string) bool) {
	for n := s.head; n != nil; {
		slot := s.lowestEmpty()
		if slot < 0 {
			return
		}
		s.checked++
		next := n.next
		if !muted(n.user) {
			s.remove(n)
			s.slots[slot] = n.user
			s.onMic[n.user] = slot
		}
		n = next
	}
}
