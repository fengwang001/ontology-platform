package ontology

import (
	"strconv"
	"sync"
)

// msgReg 是一条消息的发送/接收登记。
type msgReg struct {
	sendClock  int   // 发送事件的 Lamport 时间戳
	sender     int   // 发送节点
	sendSeq    int   // 发送节点上的事件序号
	sendVector []int // 发送事件的向量时钟快照
	received   bool  // 是否已被接收
	receiver   int   // 接收节点（未接收时为 -1）
	recvSeq    int   // 接收节点上的事件序号（未接收时为 0）
}

// System 是一个分布式逻辑时钟系统，维护 N 个节点的时钟、
// 全部已接受事件以及消息的发送/接收登记。
//
// 所有方法均支持并发调用：内部使用读写锁保证原子性，
// 被拒绝的操作不会留下任何副作用。
type System struct {
	mu        sync.RWMutex
	nodeCount int
	maxEvents int

	// clocks[node] 是该节点当前的 Lamport 逻辑时钟（标量），
	// 接收时取 max(clocks[node], 消息携带时间戳)+1。
	clocks []int
	// vectors[node] 是各节点当前的向量时钟，仅用于因果（happens-before）判定；
	// 其对角分量是本节点事件计数，与标量 Lamport 时钟分开维护。
	vectors [][]int
	// nodeEvents[node] 按节点内序号（seq-1）保存该节点的全部事件。
	nodeEvents [][]Event
	// events 按全局接受顺序保存全部事件的唯一编号序列。
	events []Event
	// messages 按消息 ID 保存发送/接收登记。
	messages map[string]msgReg
}

// NewSystem 创建一个具有 nodeCount 个节点、最多接受 maxEvents 个事件的系统。
// nodeCount<=0 返回 ErrInvalidNodeCount，maxEvents<=0 返回 ErrInvalidEventLimit。
func NewSystem(nodeCount, maxEvents int) (*System, error) {
	if nodeCount <= 0 {
		return nil, &ClockError{Kind: ErrInvalidNodeCount, Op: "new_system", Node: -1,
			Detail: "nodeCount must be positive, got " + strconv.Itoa(nodeCount)}
	}
	if maxEvents <= 0 {
		return nil, &ClockError{Kind: ErrInvalidEventLimit, Op: "new_system", Node: -1,
			Detail: "maxEvents must be positive, got " + strconv.Itoa(maxEvents)}
	}
	s := &System{
		nodeCount:  nodeCount,
		maxEvents:  maxEvents,
		clocks:     make([]int, nodeCount),
		vectors:    make([][]int, nodeCount),
		nodeEvents: make([][]Event, nodeCount),
		messages:   make(map[string]msgReg),
	}
	for i := range s.vectors {
		s.vectors[i] = make([]int, nodeCount)
	}
	return s, nil
}

// Local 在 node 上记录一个本地事件：时钟加一。
// 返回该事件的只读快照。
func (s *System) Local(node int) (Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkNode("local", node); err != nil {
		return Event{}, err
	}
	if err := s.checkCapacity("local", node, ""); err != nil {
		return Event{}, err
	}

	s.clocks[node]++
	s.vectors[node][node]++
	ev := s.appendEvent(node, Local, "", s.clocks[node], s.vectors[node])
	return ev, nil
}

// Send 在 node 上记录一个发送事件：时钟加一，并把消息 messageID
// 携带发送时的时间戳登记，供对端 Receive 取用。
func (s *System) Send(node int, messageID string) (Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkNode("send", node); err != nil {
		return Event{}, err
	}
	if messageID == "" {
		return Event{}, &ClockError{Kind: ErrInvalidMessageID, Op: "send", Node: node,
			Detail: "message id must not be empty"}
	}
	if _, exists := s.messages[messageID]; exists {
		return Event{}, &ClockError{Kind: ErrDuplicateSend, Op: "send", Node: node,
			MessageID: messageID, Detail: "message has already been sent"}
	}
	if err := s.checkCapacity("send", node, messageID); err != nil {
		return Event{}, err
	}

	s.clocks[node]++
	s.vectors[node][node]++
	seq := len(s.nodeEvents[node]) + 1
	s.messages[messageID] = msgReg{
		sendClock:  s.clocks[node],
		sender:     node,
		sendSeq:    seq,
		sendVector: cloneInts(s.vectors[node]),
		receiver:   -1,
	}
	ev := s.appendEvent(node, Send, messageID, s.clocks[node], s.vectors[node])
	return ev, nil
}

// Receive 在 node 上记录一个接收事件：时钟取
// max(本地时钟, 消息登记的发送时间戳)+1（向量时钟逐分量取 max 后本节点分量加一），
// 并标记消息已被接收。
func (s *System) Receive(node int, messageID string) (Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkNode("receive", node); err != nil {
		return Event{}, err
	}
	if messageID == "" {
		return Event{}, &ClockError{Kind: ErrInvalidMessageID, Op: "receive", Node: node,
			Detail: "message id must not be empty"}
	}
	msg, exists := s.messages[messageID]
	if !exists {
		return Event{}, &ClockError{Kind: ErrMessageNotFound, Op: "receive", Node: node,
			MessageID: messageID, Detail: "message was never sent"}
	}
	if msg.received {
		return Event{}, &ClockError{Kind: ErrDuplicateReceive, Op: "receive", Node: node,
			MessageID: messageID,
			Detail:    "message already received by node " + strconv.Itoa(msg.receiver)}
	}
	if err := s.checkCapacity("receive", node, messageID); err != nil {
		return Event{}, err
	}

	// 标量 Lamport 时钟：max(本地时钟, 消息携带时间戳)+1。
	if msg.sendClock > s.clocks[node] {
		s.clocks[node] = msg.sendClock
	}
	s.clocks[node]++

	// 向量时钟：逐分量合并发送方时钟后，本节点对角分量加一。
	// 向量时钟仅用于因果判定，与上面的标量时间戳分开维护。
	v := s.vectors[node]
	for i := 0; i < s.nodeCount; i++ {
		if msg.sendVector[i] > v[i] {
			v[i] = msg.sendVector[i]
		}
	}
	v[node]++

	seq := len(s.nodeEvents[node]) + 1
	msg.received = true
	msg.receiver = node
	msg.recvSeq = seq
	s.messages[messageID] = msg

	ev := s.appendEvent(node, Receive, messageID, s.clocks[node], v)
	return ev, nil
}

// Event 返回 (node, seq) 引用的事件快照；不存在返回 ErrUnknownEvent。
func (s *System) Event(ref EventRef) (Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ev, ok := s.lookupLocked(ref)
	if !ok {
		return Event{}, &ClockError{Kind: ErrUnknownEvent, Op: "event", Node: ref.Node,
			Detail: "no event with seq " + strconv.Itoa(ref.Seq)}
	}
	return cloneEvent(ev), nil
}

// Clocks 返回各节点当前 Lamport 时钟的副本。
func (s *System) Clocks() []int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]int, s.nodeCount)
	copy(out, s.clocks)
	return out
}

// Events 返回全部已接受事件的副本（按接受顺序，即全局事件编号顺序）。
func (s *System) Events() []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return cloneEvents(s.events)
}

// EventCount 返回已接受事件总数。
func (s *System) EventCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.events)
}

// MessageTimestamp 返回消息发送时登记的 Lamport 时间戳；未登记返回 ErrMessageNotFound。
func (s *System) MessageTimestamp(messageID string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	msg, exists := s.messages[messageID]
	if !exists {
		return 0, &ClockError{Kind: ErrMessageNotFound, Op: "message_timestamp", Node: -1,
			MessageID: messageID, Detail: "message was never sent"}
	}
	return msg.sendClock, nil
}

// ---- 内部辅助（调用方持锁）----

// checkNode 校验节点编号在 [0, nodeCount) 范围内。
func (s *System) checkNode(op string, node int) error {
	if node < 0 || node >= s.nodeCount {
		return &ClockError{Kind: ErrNodeOutOfRange, Op: op, Node: node,
			Detail: "valid range is [0," + strconv.Itoa(s.nodeCount) + ")"}
	}
	return nil
}

// checkCapacity 校验事件总数尚未超过上限。
func (s *System) checkCapacity(op string, node int, messageID string) error {
	if len(s.events) >= s.maxEvents {
		return &ClockError{Kind: ErrTooManyEvents, Op: op, Node: node, MessageID: messageID,
			Detail: "event limit " + strconv.Itoa(s.maxEvents) + " reached"}
	}
	return nil
}

// appendEvent 在锁内把事件追加到节点序列与全局序列，返回其快照副本。
// 调用方必须已完成全部校验并推进完时钟；clock 为标量 Lamport 时间戳。
func (s *System) appendEvent(node int, kind EventType, messageID string, clock int, vector []int) Event {
	seq := len(s.nodeEvents[node]) + 1
	ev := Event{
		Node:      node,
		Seq:       seq,
		Kind:      kind,
		Clock:     clock,
		MessageID: messageID,
		Vector:    cloneInts(vector),
	}
	s.nodeEvents[node] = append(s.nodeEvents[node], ev)
	s.events = append(s.events, ev)
	return cloneEvent(ev)
}

// lookupLocked 在不额外加锁的前提下按引用查找事件。
func (s *System) lookupLocked(ref EventRef) (Event, bool) {
	if ref.Node < 0 || ref.Node >= s.nodeCount || ref.Seq < 1 {
		return Event{}, false
	}
	es := s.nodeEvents[ref.Node]
	if ref.Seq > len(es) {
		return Event{}, false
	}
	return es[ref.Seq-1], true
}

// cloneInts 返回整型切片的独立副本。
func cloneInts(in []int) []int {
	out := make([]int, len(in))
	copy(out, in)
	return out
}

// cloneEvent 返回事件的深拷贝（Vector 切片独立）。
func cloneEvent(e Event) Event {
	e.Vector = cloneInts(e.Vector)
	return e
}

// cloneEvents 批量深拷贝事件。
func cloneEvents(in []Event) []Event {
	out := make([]Event, len(in))
	for i := range in {
		out[i] = cloneEvent(in[i])
	}
	return out
}
