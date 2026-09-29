package hlc

import (
	"fmt"
	"strings"
	"sync"
)

// Timestamp 是混合逻辑时钟时间戳：物理部分 l 与逻辑计数 c 的字典序对。
// 比较规则：先比 Physical，相等再比 Counter。
type Timestamp struct {
	Physical int64
	Counter  uint64
}

// Less 报告 t 是否严格早于 other。
func (t Timestamp) Less(other Timestamp) bool {
	if t.Physical != other.Physical {
		return t.Physical < other.Physical
	}
	return t.Counter < other.Counter
}

// Equal 报告两个时间戳是否相等。
func (t Timestamp) Equal(other Timestamp) bool {
	return t.Physical == other.Physical && t.Counter == other.Counter
}

// String 返回 "l.c" 形式。
func (t Timestamp) String() string { return fmt.Sprintf("%d.%d", t.Physical, t.Counter) }

// Kind 标识事件类别。
type Kind int

const (
	KindLocal Kind = iota
	KindSend
	KindReceive
)

func (k Kind) String() string {
	switch k {
	case KindLocal:
		return "local"
	case KindSend:
		return "send"
	case KindReceive:
		return "receive"
	default:
		return "unknown"
	}
}

// Event 是节点事件历史中的一条记录。
type Event struct {
	Node      string
	Seq       int64
	Kind      Kind
	Physical  int64
	Timestamp Timestamp
	MessageID int64
	Peer      string
}

// Message 是一次发送产生的、在途或已接收的消息。
type Message struct {
	ID           int64
	From         string
	To           string
	SendPhysical int64
	SendAt       Timestamp
	Delivered    bool
}

// Config 配置时钟系统的偏差与计数上限。
type Config struct {
	MaxDrift   int64
	MaxCounter uint64
}

// Clock 是多节点共享的混合逻辑时钟系统。
type Clock struct {
	mu       sync.Mutex
	config   Config
	nodes    map[string]*nodeState
	messages map[int64]*Message
	nextMsg  int64
}

type nodeState struct {
	name    string
	clock   Timestamp
	history []Event
	seq     int64
}

const (
	// DefaultMaxDrift 是允许的物理读数与逻辑时间之间的最大偏差（毫秒语义）。
	DefaultMaxDrift int64 = 1000
	// DefaultMaxCounter 是单个物理毫秒内允许的最大逻辑计数。
	DefaultMaxCounter uint64 = 4095
)

// New 创建时钟系统并注册初始节点。
func New(cfg Config, nodes ...string) *Clock {
	if cfg.MaxDrift <= 0 {
		cfg.MaxDrift = DefaultMaxDrift
	}
	if cfg.MaxCounter == 0 {
		cfg.MaxCounter = DefaultMaxCounter
	}
	c := &Clock{
		config:   cfg,
		nodes:    make(map[string]*nodeState),
		messages: make(map[int64]*Message),
		nextMsg:  1,
	}
	for _, n := range nodes {
		name := strings.TrimSpace(n)
		if name == "" {
			continue
		}
		c.nodes[name] = &nodeState{name: name}
	}
	return c
}

// AddNode 注册一个新节点。
func (c *Clock) AddNode(node string) error {
	node = strings.TrimSpace(node)
	if node == "" {
		return &RejectError{Reason: ReasonInvalidArgument, Op: "add_node", Detail: "empty node name"}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.nodes[node]; ok {
		return &RejectError{Reason: ReasonInvalidArgument, Op: "add_node", Detail: "duplicate node: " + node}
	}
	c.nodes[node] = &nodeState{name: node}
	return nil
}

// LocalTick 在 node 上产生一个本地事件，读数为 physical。
func (c *Clock) LocalTick(node string, physical int64) (Timestamp, error) {
	node = strings.TrimSpace(node)
	if node == "" {
		return Timestamp{}, &RejectError{Reason: ReasonInvalidArgument, Op: "local", Detail: "empty node name"}
	}
	if physical < 0 {
		return Timestamp{}, &RejectError{Reason: ReasonNegativePhysical, Op: "local",
			Detail: fmt.Sprintf("physical=%d", physical)}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	st, ok := c.nodes[node]
	if !ok {
		return Timestamp{}, &RejectError{Reason: ReasonNodeNotFound, Op: "local", Detail: "node=" + node}
	}
	next := advanceLocal(st.clock, physical)
	if err := validateNext("local", c.config, st.clock, next, physical, Timestamp{}); err != nil {
		return Timestamp{}, err
	}
	st.clock = next
	c.appendEvent(st, KindLocal, physical, next, 0, "")
	return next, nil
}

// Send 在 from 上产生发送事件并生成一条目标为 to 的在途消息。
func (c *Clock) Send(from, to string, physical int64) (Message, error) {
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	if from == "" || to == "" {
		return Message{}, &RejectError{Reason: ReasonInvalidArgument, Op: "send", Detail: "empty node name"}
	}
	if physical < 0 {
		return Message{}, &RejectError{Reason: ReasonNegativePhysical, Op: "send",
			Detail: fmt.Sprintf("physical=%d", physical)}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	sender, ok := c.nodes[from]
	if !ok {
		return Message{}, &RejectError{Reason: ReasonNodeNotFound, Op: "send", Detail: "from=" + from}
	}
	if _, ok := c.nodes[to]; !ok {
		return Message{}, &RejectError{Reason: ReasonNodeNotFound, Op: "send", Detail: "to=" + to}
	}
	next := advanceLocal(sender.clock, physical)
	if err := validateNext("send", c.config, sender.clock, next, physical, Timestamp{}); err != nil {
		return Message{}, err
	}
	sender.clock = next
	msg := &Message{
		ID:           c.nextMsg,
		From:         from,
		To:           to,
		SendPhysical: physical,
		SendAt:       next,
		Delivered:    false,
	}
	c.nextMsg++
	c.messages[msg.ID] = msg
	c.appendEvent(sender, KindSend, physical, next, msg.ID, to)
	return *msg, nil
}

// Receive 在 to 上产生接收事件，消息只能被目标节点恰好接收一次。
func (c *Clock) Receive(node string, messageID int64, physical int64) (Timestamp, error) {
	node = strings.TrimSpace(node)
	if node == "" {
		return Timestamp{}, &RejectError{Reason: ReasonInvalidArgument, Op: "receive", Detail: "empty node name"}
	}
	if messageID <= 0 {
		return Timestamp{}, &RejectError{Reason: ReasonMessageNotFound, Op: "receive",
			Detail: fmt.Sprintf("message id=%d", messageID)}
	}
	if physical < 0 {
		return Timestamp{}, &RejectError{Reason: ReasonNegativePhysical, Op: "receive",
			Detail: fmt.Sprintf("physical=%d", physical)}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	st, ok := c.nodes[node]
	if !ok {
		return Timestamp{}, &RejectError{Reason: ReasonNodeNotFound, Op: "receive", Detail: "node=" + node}
	}
	msg, ok := c.messages[messageID]
	if !ok {
		return Timestamp{}, &RejectError{Reason: ReasonMessageNotFound, Op: "receive",
			Detail: fmt.Sprintf("message id=%d", messageID)}
	}
	if msg.Delivered {
		return Timestamp{}, &RejectError{Reason: ReasonMessageAlreadyReceived, Op: "receive",
			Detail: fmt.Sprintf("message id=%d already delivered", messageID)}
	}
	if msg.To != node {
		return Timestamp{}, &RejectError{Reason: ReasonTargetMismatch, Op: "receive",
			Detail: fmt.Sprintf("message id=%d targets %s, not %s", messageID, msg.To, node)}
	}
	next := advanceReceive(st.clock, msg.SendAt, physical)
	if err := validateNext("receive", c.config, st.clock, next, physical, msg.SendAt); err != nil {
		return Timestamp{}, err
	}
	st.clock = next
	msg.Delivered = true
	c.appendEvent(st, KindReceive, physical, next, msg.ID, msg.From)
	return next, nil
}

// History 返回 node 的事件历史（按时间戳严格有序）。
func (c *Clock) History(node string) ([]Event, error) {
	node = strings.TrimSpace(node)
	if node == "" {
		return nil, &RejectError{Reason: ReasonInvalidArgument, Op: "history", Detail: "empty node name"}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	st, ok := c.nodes[node]
	if !ok {
		return nil, &RejectError{Reason: ReasonNodeNotFound, Op: "history", Detail: "node=" + node}
	}
	out := make([]Event, len(st.history))
	copy(out, st.history)
	return out, nil
}

// PendingMessages 返回尚未被接收的在途消息快照。
func (c *Clock) PendingMessages() []Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []Message
	for i := int64(1); i < c.nextMsg; i++ {
		if m := c.messages[i]; m != nil && !m.Delivered {
			out = append(out, *m)
		}
	}
	return out
}

// LookupMessage 按号查询消息。
func (c *Clock) LookupMessage(id int64) (Message, error) {
	if id <= 0 {
		return Message{}, &RejectError{Reason: ReasonMessageNotFound, Op: "lookup_message",
			Detail: fmt.Sprintf("message id=%d", id)}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.messages[id]
	if !ok {
		return Message{}, &RejectError{Reason: ReasonMessageNotFound, Op: "lookup_message",
			Detail: fmt.Sprintf("message id=%d", id)}
	}
	return *m, nil
}

// Now 返回 node 当前时间戳。
func (c *Clock) Now(node string) (Timestamp, error) {
	node = strings.TrimSpace(node)
	if node == "" {
		return Timestamp{}, &RejectError{Reason: ReasonInvalidArgument, Op: "now", Detail: "empty node name"}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	st, ok := c.nodes[node]
	if !ok {
		return Timestamp{}, &RejectError{Reason: ReasonNodeNotFound, Op: "now", Detail: "node=" + node}
	}
	return st.clock, nil
}

// advanceLocal 计算本地/发送事件的候选时间戳（尚未校验偏差与计数上限）。
// 规则：l' = max(l, pt)；l' == l 时 c' = c+1，否则 c' = 0。
func advanceLocal(cur Timestamp, physical int64) Timestamp {
	if physical > cur.Physical {
		return Timestamp{Physical: physical, Counter: 0}
	}
	return Timestamp{Physical: cur.Physical, Counter: cur.Counter + 1}
}

// advanceReceive 计算接收事件的候选时间戳（尚未校验偏差与计数上限）。
// 规则（标准 HLC 合并）：
//
//	pt > max(l, lm): l'=pt, c'=0
//	l  > max(lm, pt): l'=l,  c'=c+1
//	lm > max(l, pt): l'=lm, c'=cm+1
//	三者相等:        l'=l,  c'=max(c, cm)+1
func advanceReceive(local, remote Timestamp, physical int64) Timestamp {
	switch {
	case physical > local.Physical && physical > remote.Physical:
		return Timestamp{Physical: physical, Counter: 0}
	case local.Physical > remote.Physical && local.Physical >= physical:
		return Timestamp{Physical: local.Physical, Counter: local.Counter + 1}
	case remote.Physical > local.Physical && remote.Physical >= physical:
		return Timestamp{Physical: remote.Physical, Counter: remote.Counter + 1}
	default:
		c := local.Counter
		if remote.Counter > c {
			c = remote.Counter
		}
		return Timestamp{Physical: local.Physical, Counter: c + 1}
	}
}

// validateNext 在不修改任何状态的前提下校验候选时间戳的偏差与计数上限。
func validateNext(op string, cfg Config, cur, next Timestamp, physical int64, remote Timestamp) error {
	if next.Physical-physical > cfg.MaxDrift {
		return &RejectError{
			Reason: ReasonDriftExceeded, Op: op,
			Detail: fmt.Sprintf("logical physical %d leads reading %d by %d > max %d",
				next.Physical, physical, next.Physical-physical, cfg.MaxDrift),
		}
	}
	if next.Counter > cfg.MaxCounter {
		return &RejectError{
			Reason: ReasonCounterOverflow, Op: op,
			Detail: fmt.Sprintf("counter %d > max %d at physical %d",
				next.Counter, cfg.MaxCounter, next.Physical),
		}
	}
	return nil
}

func (c *Clock) appendEvent(st *nodeState, kind Kind, physical int64, ts Timestamp, msgID int64, peer string) {
	st.seq++
	st.history = append(st.history, Event{
		Node:      st.name,
		Seq:       st.seq,
		Kind:      kind,
		Physical:  physical,
		Timestamp: ts,
		MessageID: msgID,
		Peer:      peer,
	})
}
