// Package outbox 实现事务发件箱（transactional outbox）中继组件。
//
// 业务事务提交时将消息写入发件箱并分配提交序；中继按提交序与写入序
// 逐条“先投递、后标记”；崩溃重启后未标记的消息会被重投，下游按消息
// 标识幂等去重，从而保证既不丢消息也不多应用。
package outbox

import (
	"errors"
	"fmt"
	"sync"
)

// Reason 区分操作被拒绝的原因。
type Reason string

const (
	// ReasonTxUnavailable 事务不可用：不存在、已提交或已中止。
	ReasonTxUnavailable Reason = "tx_unavailable"
	// ReasonInvalidMessage 消息非法：载荷为空或超过最大长度。
	ReasonInvalidMessage Reason = "invalid_message"
	// ReasonBacklogFull 提交后待投积压将超过上限。
	ReasonBacklogFull Reason = "backlog_full"
)

// Error 是发件箱拒绝操作时返回的错误，携带可区分的原因。
type Error struct {
	Reason Reason
	Op     string
	Detail string
}

func (e *Error) Error() string {
	return fmt.Sprintf("outbox: %s rejected (%s): %s", e.Op, e.Reason, e.Detail)
}

// IsReason 判断 err 是否为指定原因的拒绝。
func IsReason(err error, r Reason) bool {
	var e *Error
	return errors.As(err, &e) && e.Reason == r
}

// MaxPayloadBytes 是单条消息载荷的最大字节数。
const MaxPayloadBytes = 4096

// Message 是发件箱中的一条消息。
type Message struct {
	ID        uint64 // 全局唯一、单调递增的消息标识
	Tx        string // 所属事务名
	Seq       uint64 // 事务内写入序
	CommitSeq uint64 // 提交序（提交时分配）
	Payload   string
}

// txStatus 表示事务生命周期状态。
type txStatus int

const (
	txOpen txStatus = iota
	txCommitted
	txAborted
)

// txState 是事务的内部状态。
type txState struct {
	status txStatus
	msgs   []Message // 提交前缓冲的写入
}

// Event 记录一次被接受的、会影响投递结果的操作，
// 用于并发测试中与朴素参照模型对拍。
type Event struct {
	Op        string // "commit" 或 "abort"
	Tx        string
	CommitSeq uint64
	Msgs      []Message // 仅 commit 事件携带
}

// Store 是发件箱的持久状态（内存实现，模拟可崩溃恢复的存储）。
// 所有方法均可并发调用。
type Store struct {
	mu         sync.Mutex
	txs        map[string]*txState
	nextMsgID  uint64 // 下一个消息标识
	commitSeq  uint64 // 已分配的提交序
	maxPending int    // 待投积压上限
	pending    []Message
	marked     map[uint64]bool
	events     []Event
}

// NewStore 创建发件箱存储，maxPending 为待投积压上限。
func NewStore(maxPending int) *Store {
	return &Store{
		txs:        make(map[string]*txState),
		maxPending: maxPending,
		marked:     make(map[uint64]bool),
	}
}

// Begin 以名字开启一个事务；同名事务仍处于活动状态时拒绝。
func (s *Store) Begin(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if tx, ok := s.txs[name]; ok && tx.status == txOpen {
		return &Error{Reason: ReasonTxUnavailable, Op: "begin",
			Detail: fmt.Sprintf("transaction %q is already open", name)}
	}
	s.txs[name] = &txState{status: txOpen}
	return nil
}

// Write 向活动事务追加一条消息。
func (s *Store) Write(tx, payload string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.txs[tx]
	if !ok || state.status != txOpen {
		return &Error{Reason: ReasonTxUnavailable, Op: "write",
			Detail: fmt.Sprintf("transaction %q is not open", tx)}
	}
	if payload == "" {
		return &Error{Reason: ReasonInvalidMessage, Op: "write",
			Detail: "payload is empty"}
	}
	if len(payload) > MaxPayloadBytes {
		return &Error{Reason: ReasonInvalidMessage, Op: "write",
			Detail: fmt.Sprintf("payload of %d bytes exceeds limit %d", len(payload), MaxPayloadBytes)}
	}
	s.nextMsgID++
	state.msgs = append(state.msgs, Message{
		ID:      s.nextMsgID,
		Tx:      tx,
		Seq:     uint64(len(state.msgs)),
		Payload: payload,
	})
	return nil
}

// Commit 提交事务：分配提交序，把事务内消息按写入序移入待投队列。
// 若提交后待投积压将超过上限，则拒绝提交且事务保持打开，
// 不改变提交序计数器、待投队列与下游状态。
func (s *Store) Commit(tx string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.txs[tx]
	if !ok || state.status != txOpen {
		return &Error{Reason: ReasonTxUnavailable, Op: "commit",
			Detail: fmt.Sprintf("transaction %q is not open", tx)}
	}
	if len(s.pending)+len(state.msgs) > s.maxPending {
		return &Error{Reason: ReasonBacklogFull, Op: "commit",
			Detail: fmt.Sprintf("pending backlog %d + %d new messages exceeds limit %d",
				len(s.pending), len(state.msgs), s.maxPending)}
	}
	s.commitSeq++
	msgs := make([]Message, len(state.msgs))
	for i, m := range state.msgs {
		m.CommitSeq = s.commitSeq
		msgs[i] = m
	}
	s.pending = append(s.pending, msgs...)
	state.status = txCommitted
	state.msgs = nil
	s.events = append(s.events, Event{Op: "commit", Tx: tx, CommitSeq: s.commitSeq, Msgs: msgs})
	return nil
}

// Abort 中止事务：丢弃其全部消息，这些消息永不投递。
func (s *Store) Abort(tx string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.txs[tx]
	if !ok || state.status != txOpen {
		return &Error{Reason: ReasonTxUnavailable, Op: "abort",
			Detail: fmt.Sprintf("transaction %q is not open", tx)}
	}
	state.status = txAborted
	state.msgs = nil
	s.events = append(s.events, Event{Op: "abort", Tx: tx})
	return nil
}

// Pending 返回当前全部已提交未标记的消息（按提交序与写入序）。
func (s *Store) Pending() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Message, len(s.pending))
	copy(out, s.pending)
	return out
}

// Mark 将消息标记为已投递。
func (s *Store) Mark(id uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.marked[id] {
		return
	}
	s.marked[id] = true
	for i, m := range s.pending {
		if m.ID == id {
			s.pending = append(s.pending[:i], s.pending[i+1:]...)
			return
		}
	}
}

// PendingLen 返回待投积压长度。
func (s *Store) PendingLen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending)
}

// Events 返回已接受操作的序列化日志（供参照模型回放）。
func (s *Store) Events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event, len(s.events))
	copy(out, s.events)
	return out
}
