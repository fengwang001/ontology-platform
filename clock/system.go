package clock

import (
	"fmt"
	"log"
	"sort"
	"sync"
)

// Logger 是组件接受的最小日志接口；标准库 *log.Logger 天然满足。
type Logger interface {
	Printf(format string, v ...any)
}

// message 是一次 Send 登记的在途消息。
type message struct {
	id       int64
	clock    int64 // 发送事件携带的逻辑时间戳
	from     int   // 发送节点
	sendEvID int64 // 发送事件的全局 ID
	recvEvID int64 // 接收事件的全局 ID；-1 表示尚未被接收
	recvNode int   // 接收节点；未接收时为 -1
}

// eventRec 是事件的内部记录，在对外 Event 之外补充因果边。
type eventRec struct {
	ev     Event
	recvID int64 // 若该事件是 send 且其消息已被接收，指向 receive 事件全局 ID；否则 -1
}

// System 是多节点分布式逻辑时钟组件。
//
// 所有方法均可被多个 goroutine 并发调用；一把读写锁串行化全部写操作，
// 校验与状态变更在同一临界区内原子完成，因此：
//   - 每个节点的节点内事件序号（Seq）连续；
//   - 全序只取决于 (Clock, Node)，与操作被接受的先后无关；
//   - 被拒绝的操作不改变任何时钟、编号、全序与消息登记。
type System struct {
	mu        sync.RWMutex
	nodeCount int
	maxEvents int64

	clocks []int64       // 每个节点的当前逻辑时钟
	counts []int64       // 每个节点已接受的事件数（也是下一事件的 Seq）
	byNode [][]*eventRec // 每个节点按节点内顺序排列的事件
	all    []*eventRec   // 全部事件，按被接受顺序排列；下标即全局 ID

	messages  map[int64]*message // 已登记消息
	nextMsgID int64              // 下一发送事件分配的消息编号

	logf func(format string, v ...any)
}

// New 创建一个含 nodeCount 个节点、最多容纳 maxEvents 个被接受事件的系统。
// nodeCount <= 0 或 maxEvents < 0 属于编程错误，直接 panic。
// 默认日志走标准库 log；可用 SetLogger 替换或关闭。
func New(nodeCount int, maxEvents int64) *System {
	if nodeCount <= 0 {
		panic(fmt.Sprintf("clock: New: nodeCount must be positive, got %d", nodeCount))
	}
	if maxEvents < 0 {
		panic(fmt.Sprintf("clock: New: maxEvents must be non-negative, got %d", maxEvents))
	}
	return &System{
		nodeCount: nodeCount,
		maxEvents: maxEvents,
		clocks:    make([]int64, nodeCount),
		counts:    make([]int64, nodeCount),
		byNode:    make([][]*eventRec, nodeCount),
		messages:  make(map[int64]*message),
		nextMsgID: 0,
		logf:      log.Printf,
	}
}

// SetLogger 替换日志输出；传 nil 关闭日志。
func (s *System) SetLogger(l Logger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if l == nil {
		s.logf = func(string, ...any) {}
	} else {
		s.logf = l.Printf
	}
}

// NodeCount 返回节点数。
func (s *System) NodeCount() int { return s.nodeCount }

// EventCount 返回当前已接受事件总数。
func (s *System) EventCount() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return int64(len(s.all))
}

func newErr(kind ErrorKind, op string, node int, msgID int64, detail string) *Error {
	return &Error{Kind: kind, Op: op, Node: node, MessageID: msgID, Detail: detail}
}

// Local 在 node 上产生一个本地事件：clock' = clock + 1。
func (s *System) Local(node int) (*Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkNode("local", node); err != nil {
		s.rejectLog("local", node, -1, err)
		return nil, err
	}
	if err := s.checkLimit("local", node, -1); err != nil {
		s.rejectLog("local", node, -1, err)
		return nil, err
	}

	old := s.clocks[node]
	c := old + 1
	rec := s.appendEvent(node, c, EventLocal, -1)
	s.clocks[node] = c

	s.logf("input=local node=%d | accept id=%d seq=%d timestamp=%d | rule: local clock %d + 1 = %d",
		node, rec.ev.ID, rec.ev.Seq, c, old, c)
	return copyEvent(&rec.ev), nil
}

// Send 在 node 上产生一个发送事件：clock' = clock + 1，
// 并原子登记一条携带该时间戳的消息，返回的事件中带 MessageID。
func (s *System) Send(node int) (*Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkNode("send", node); err != nil {
		s.rejectLog("send", node, -1, err)
		return nil, err
	}
	if err := s.checkLimit("send", node, -1); err != nil {
		s.rejectLog("send", node, -1, err)
		return nil, err
	}

	old := s.clocks[node]
	c := old + 1
	msgID := s.nextMsgID
	rec := s.appendEvent(node, c, EventSend, msgID)
	s.clocks[node] = c

	s.messages[msgID] = &message{
		id:       msgID,
		clock:    c,
		from:     node,
		sendEvID: rec.ev.ID,
		recvEvID: -1,
		recvNode: -1,
	}
	s.nextMsgID++

	s.logf("input=send node=%d | accept id=%d seq=%d timestamp=%d message=%d | rule: send clock %d + 1 = %d; message registered",
		node, rec.ev.ID, rec.ev.Seq, c, msgID, old, c)
	return copyEvent(&rec.ev), nil
}

// Receive 在 node 上接收 messageID：clock' = max(clock, message.clock) + 1。
// 消息必须已登记且未被接收，整批校验通过后状态才会变更。
func (s *System) Receive(node int, messageID int64) (*Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkNode("receive", node); err != nil {
		s.rejectLog("receive", node, messageID, err)
		return nil, err
	}
	msg, ok := s.messages[messageID]
	if !ok {
		err := newErr(KindMessageNotFound, "receive", node, messageID,
			"message is not registered")
		s.rejectLog("receive", node, messageID, err)
		return nil, err
	}
	if msg.recvEvID != -1 {
		err := newErr(KindDuplicateReceive, "receive", node, messageID,
			fmt.Sprintf("message already received by node %d at event %d", msg.recvNode, msg.recvEvID))
		s.rejectLog("receive", node, messageID, err)
		return nil, err
	}
	if err := s.checkLimit("receive", node, messageID); err != nil {
		s.rejectLog("receive", node, messageID, err)
		return nil, err
	}

	old := s.clocks[node]
	c := old
	takenFrom := "local"
	if msg.clock > c {
		c = msg.clock
		takenFrom = "message"
	}
	c++
	rec := s.appendEvent(node, c, EventReceive, messageID)
	s.clocks[node] = c

	msg.recvEvID = rec.ev.ID
	msg.recvNode = node
	s.all[msg.sendEvID].recvID = rec.ev.ID

	s.logf("input=receive node=%d message=%d | accept id=%d seq=%d timestamp=%d | rule: max(local=%d, message=%d)=%d (%s) + 1 = %d; causal edge send(event=%d,node=%d) -> receive(event=%d)",
		node, messageID, rec.ev.ID, rec.ev.Seq, c, old, msg.clock, c-1, takenFrom, c,
		msg.sendEvID, msg.from, rec.ev.ID)
	return copyEvent(&rec.ev), nil
}

// checkNode / checkLimit 只做只读校验；调用方持锁。
func (s *System) checkNode(op string, node int) *Error {
	if node < 0 || node >= s.nodeCount {
		return newErr(KindNodeOutOfRange, op, node, -1,
			fmt.Sprintf("valid nodes are [0,%d)", s.nodeCount))
	}
	return nil
}

func (s *System) checkLimit(op string, node int, msgID int64) *Error {
	if int64(len(s.all)) >= s.maxEvents {
		return newErr(KindEventLimitExceeded, op, node, msgID,
			fmt.Sprintf("event count %d reached limit %d", len(s.all), s.maxEvents))
	}
	return nil
}

// appendEvent 在已持锁的前提下登记事件并返回内部记录。
func (s *System) appendEvent(node int, clock int64, typ EventType, msgID int64) *eventRec {
	id := int64(len(s.all))
	seq := s.counts[node]
	rec := &eventRec{
		ev: Event{
			ID:        id,
			Node:      node,
			Seq:       seq,
			Clock:     clock,
			Type:      typ,
			MessageID: msgID,
		},
		recvID: -1,
	}
	s.byNode[node] = append(s.byNode[node], rec)
	s.all = append(s.all, rec)
	s.counts[node]++
	return rec
}

func (s *System) rejectLog(op string, node int, msgID int64, err *Error) {
	s.logf("input=%s node=%d message=%d | REJECT kind=%s detail=%q | no state changed",
		op, node, msgID, err.Kind, err.Detail)
}

// TotalOrder 返回全部已接受事件的全序副本：
// 先按逻辑时钟升序，时钟并列时按节点编号升序，再以全局 ID 兜底。
// 同一节点的时钟严格递增，因此前两键足以确定全序；ID 兜底仅为稳健性。
func (s *System) TotalOrder() []*Event {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]*Event, len(s.all))
	for i, rec := range s.all {
		out[i] = copyEvent(&rec.ev)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Clock != out[j].Clock {
			return out[i].Clock < out[j].Clock
		}
		if out[i].Node != out[j].Node {
			return out[i].Node < out[j].Node
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Compare 判定事件 a 与事件 b 的因果关系：
//   - 同一事件视为 OrderBefore（happened-before 的自反形式）；
//   - 同一节点上 Seq 小的先于 Seq 大的；
//   - send 事件先于其消息的 receive 事件；
//   - 以上关系取传递闭包；互不可达即为并发。
func (s *System) Compare(a, b int64) (Order, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	n := int64(len(s.all))
	if a < 0 || a >= n || b < 0 || b >= n {
		return "", fmt.Errorf("clock: Compare: event id out of range: a=%d b=%d, have %d events", a, b, n)
	}
	if a == b {
		s.logf("compare a=%d b=%d | same event -> before (reflexive)", a, b)
		return OrderBefore, nil
	}

	if path := s.findPath(a, b); path != nil {
		s.logf("compare a=%d b=%d | before | path: %s", a, b, formatPath(path))
		return OrderBefore, nil
	}
	if path := s.findPath(b, a); path != nil {
		s.logf("compare a=%d b=%d | after | reverse path: %s", a, b, formatPath(path))
		return OrderAfter, nil
	}
	s.logf("compare a=%d b=%d | concurrent | neither reachable via same-node order or send->receive edges", a, b)
	return OrderConcurrent, nil
}

// successors 返回持锁状态下事件 cur 的直接因果后继：
// 同一节点上的下一事件，以及（若 cur 是发送事件）对应接收事件。
func (s *System) successors(rec *eventRec) []int64 {
	var nx []int64
	if next := rec.ev.Seq + 1; next < int64(len(s.byNode[rec.ev.Node])) {
		nx = append(nx, s.byNode[rec.ev.Node][next].ev.ID)
	}
	if rec.recvID != -1 {
		nx = append(nx, rec.recvID)
	}
	return nx
}

// findPath 用 BFS 在因果后继图上寻找从 from 到 to 的一条传递路径，
// 返回事件 ID 序列；不可达时返回 nil。
func (s *System) findPath(from, to int64) []int64 {
	n := len(s.all)
	parent := make([]int64, n)
	seen := make([]bool, n)
	for i := range parent {
		parent[i] = -1
	}
	seen[from] = true
	queue := []int64{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == to {
			var path []int64
			for x := to; x != -1; x = parent[x] {
				path = append(path, x)
			}
			for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
				path[i], path[j] = path[j], path[i]
			}
			return path
		}
		for _, nx := range s.successors(s.all[cur]) {
			if !seen[nx] {
				seen[nx] = true
				parent[nx] = cur
				queue = append(queue, nx)
			}
		}
	}
	return nil
}

func formatPath(path []int64) string {
	b := make([]byte, 0, len(path)*8)
	for i, id := range path {
		if i > 0 {
			b = append(b, " -> "...)
		}
		b = fmt.Appendf(b, "event%d", id)
	}
	return string(b)
}

// Snapshot 返回某节点当前时钟与已接受事件数。
func (s *System) Snapshot(node int) (clockValue int64, eventCount int64, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if e := s.checkNode("snapshot", node); e != nil {
		return 0, 0, e
	}
	return s.clocks[node], s.counts[node], nil
}

// copyEvent 返回事件值副本，避免调用方修改内部状态。
func copyEvent(e *Event) *Event {
	cp := *e
	return &cp
}
