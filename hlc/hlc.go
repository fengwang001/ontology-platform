// Package hlc 实现混合逻辑时钟（Hybrid Logical Clock）及其事件历史。
//
// 每个节点维护 (logical, counter) 组成的当前时间。本地/发送事件按
// max(logical, physical) 推进，未推进时计数加一；接收事件再并入消息
// 携带的时间戳。物理时钟回拨时逻辑时间不回退，保证时间戳严格单调。
package hlc

import (
	"fmt"
	"sync"
)

// Timestamp 是混合逻辑时钟时间戳，按 (Logical, Counter) 字典序比较。
type Timestamp struct {
	Logical int64
	Counter uint64
}

// Compare 比较两个时间戳，t<o 返回 -1，t==o 返回 0，t>o 返回 1。
func (t Timestamp) Compare(o Timestamp) int {
	switch {
	case t.Logical != o.Logical:
		if t.Logical < o.Logical {
			return -1
		}
		return 1
	case t.Counter != o.Counter:
		if t.Counter < o.Counter {
			return -1
		}
		return 1
	default:
		return 0
	}
}

func (t Timestamp) String() string {
	return fmt.Sprintf("%d.%d", t.Logical, t.Counter)
}

// EventKind 标识事件类型：本地、发送、接收。
type EventKind int

const (
	EventLocal EventKind = iota
	EventSend
	EventReceive
)

func (k EventKind) String() string {
	switch k {
	case EventLocal:
		return "local"
	case EventSend:
		return "send"
	case EventReceive:
		return "receive"
	default:
		return "unknown"
	}
}

// Event 是节点事件历史中的一条记录。
type Event struct {
	Seq       uint64    // 节点内单调递增的事件序号
	Kind      EventKind // 事件类型
	Timestamp Timestamp // 分配的时间戳
	Physical  int64     // 事件发生时的物理读数
	MessageID string    // 发送/接收事件关联的消息号
	Peer      string    // 对端节点
}

// Config 是时钟网络的配置。
type Config struct {
	// MaxClockOffset 物理读数允许超前节点当前逻辑时间的最大值；超出即拒绝。
	MaxClockOffset int64
	// MaxCounter 计数器上限；需要继续自增而达到上限时拒绝。
	MaxCounter uint64
}

// message 是在途消息。
type message struct {
	id        string
	from      string
	to        string
	ts        Timestamp
	delivered bool
}

// node 是单个节点的时钟状态与事件历史。
type node struct {
	logical int64
	counter uint64
	nextSeq uint64
	history []Event
}

// Network 管理一组节点及节点间的在途消息。
// 全部操作由互斥锁串行化，可线性化，支持多执行体并发调用；
// 同一事件序列在并发下得到的时间戳一致。
type Network struct {
	mu     sync.Mutex
	cfg    Config
	nodes  map[string]*node
	msgs   map[string]*message
	msgSeq uint64
}

// NewNetwork 创建时钟网络。
func NewNetwork(cfg Config) *Network {
	return &Network{
		cfg:   cfg,
		nodes: make(map[string]*node),
		msgs:  make(map[string]*message),
	}
}

// AddNode 注册一个节点。
func (n *Network) AddNode(id string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if id == "" {
		return ErrEmptyNodeID
	}
	if _, ok := n.nodes[id]; ok {
		return ErrNodeExists
	}
	n.nodes[id] = &node{}
	return nil
}

// checkPhysical 校验物理读数：非负且未超前当前逻辑时间超过上限。
// 节点尚无事件时没有参照基准，跳过偏差校验。
func (n *Network) checkPhysical(nd *node, physical int64) error {
	if physical < 0 {
		return ErrNegativePhysical
	}
	if nd.nextSeq > 0 && physical-nd.logical > n.cfg.MaxClockOffset {
		return ErrClockOffsetExceeded
	}
	return nil
}

// tick 推进本地/发送事件的时钟，返回新时间戳；不修改节点状态，
// 由调用方在校验全部通过后提交。
func (n *Network) tick(nd *node, physical int64) (Timestamp, error) {
	ts := Timestamp{Logical: max(nd.logical, physical)}
	if ts.Logical == nd.logical {
		if nd.counter >= n.cfg.MaxCounter {
			return Timestamp{}, ErrCounterOverflow
		}
		ts.Counter = nd.counter + 1
	}
	return ts, nil
}

// merge 推进接收事件的时钟：并入消息时间戳，规则同 HLC，
// 但当消息时间占主导时计数取消息计数加一，保证接收事件严格大于发送事件。
// 不修改节点状态，由调用方在校验全部通过后提交。
func (n *Network) merge(nd *node, msgTS Timestamp, physical int64) (Timestamp, error) {
	ts := Timestamp{Logical: max(nd.logical, msgTS.Logical, physical)}
	switch {
	case ts.Logical == nd.logical && ts.Logical == msgTS.Logical:
		ts.Counter = max(nd.counter, msgTS.Counter) + 1
	case ts.Logical == nd.logical:
		ts.Counter = nd.counter + 1
	default: // 消息时间占主导（物理读数不可能超过前两者而不触发偏差拒绝）
		ts.Counter = msgTS.Counter + 1
	}
	if ts.Counter > n.cfg.MaxCounter {
		return Timestamp{}, ErrCounterOverflow
	}
	return ts, nil
}

// commit 将新时间戳写入节点并追加事件历史。
func (n *Network) commit(nd *node, ts Timestamp, ev Event) {
	nd.logical = ts.Logical
	nd.counter = ts.Counter
	nd.nextSeq++
	ev.Seq = nd.nextSeq
	ev.Timestamp = ts
	nd.history = append(nd.history, ev)
}

// Local 在节点上产生一个本地事件，返回分配的时间戳。
func (n *Network) Local(nodeID string, physical int64) (Timestamp, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	nd, ok := n.nodes[nodeID]
	if !ok {
		return Timestamp{}, ErrNodeNotFound
	}
	if err := n.checkPhysical(nd, physical); err != nil {
		return Timestamp{}, err
	}
	ts, err := n.tick(nd, physical)
	if err != nil {
		return Timestamp{}, err
	}
	n.commit(nd, ts, Event{Kind: EventLocal, Physical: physical})
	return ts, nil
}

// Send 在 from 节点上产生一个发送事件，创建发往 to 的在途消息，
// 返回消息号与发送时间戳。
func (n *Network) Send(from, to string, physical int64) (string, Timestamp, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	nd, ok := n.nodes[from]
	if !ok {
		return "", Timestamp{}, ErrNodeNotFound
	}
	if _, ok := n.nodes[to]; !ok {
		return "", Timestamp{}, ErrNodeNotFound
	}
	if err := n.checkPhysical(nd, physical); err != nil {
		return "", Timestamp{}, err
	}
	ts, err := n.tick(nd, physical)
	if err != nil {
		return "", Timestamp{}, err
	}
	n.msgSeq++
	msgID := fmt.Sprintf("m%d", n.msgSeq)
	n.commit(nd, ts, Event{Kind: EventSend, Physical: physical, MessageID: msgID, Peer: to})
	n.msgs[msgID] = &message{id: msgID, from: from, to: to, ts: ts}
	return msgID, ts, nil
}

// Receive 在 nodeID 节点上接收消息 msgID，产生接收事件并返回时间戳。
// 每条消息只能被其目标节点恰好接收一次。
func (n *Network) Receive(nodeID, msgID string, physical int64) (Timestamp, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	nd, ok := n.nodes[nodeID]
	if !ok {
		return Timestamp{}, ErrNodeNotFound
	}
	if msgID == "" {
		return Timestamp{}, ErrEmptyMessageID
	}
	msg, ok := n.msgs[msgID]
	if !ok {
		return Timestamp{}, ErrMessageNotFound
	}
	if msg.delivered {
		return Timestamp{}, ErrMessageAlreadyReceived
	}
	if msg.to != nodeID {
		return Timestamp{}, ErrNotRecipient
	}
	if err := n.checkPhysical(nd, physical); err != nil {
		return Timestamp{}, err
	}
	ts, err := n.merge(nd, msg.ts, physical)
	if err != nil {
		return Timestamp{}, err
	}
	msg.delivered = true
	n.commit(nd, ts, Event{Kind: EventReceive, Physical: physical, MessageID: msgID, Peer: msg.from})
	return ts, nil
}

// History 返回节点按时间戳升序排列的事件历史副本。
func (n *Network) History(nodeID string) ([]Event, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	nd, ok := n.nodes[nodeID]
	if !ok {
		return nil, ErrNodeNotFound
	}
	out := make([]Event, len(nd.history))
	copy(out, nd.history)
	return out, nil
}

// Now 返回节点当前时间（不产生事件）。
func (n *Network) Now(nodeID string) (Timestamp, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	nd, ok := n.nodes[nodeID]
	if !ok {
		return Timestamp{}, ErrNodeNotFound
	}
	return Timestamp{Logical: nd.logical, Counter: nd.counter}, nil
}
