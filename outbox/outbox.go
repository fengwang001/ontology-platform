// Package outbox 实现事务发件箱：业务事务提交时写入的消息，
// 由中继按提交顺序投递给下游，投递成功后标记。
package outbox

import (
	"errors"
	"sync"
)

// 可区分的拒绝原因，调用方用 errors.Is 判定。
var (
	ErrTxExists        = errors.New("outbox: transaction already exists")
	ErrTxNotFound      = errors.New("outbox: transaction not found")
	ErrTxNotActive     = errors.New("outbox: transaction not active")
	ErrInvalidMessage  = errors.New("outbox: invalid message")
	ErrBacklogExceeded = errors.New("outbox: pending backlog exceeded")
)

// Message 是业务事务写入发件箱的消息。
type Message struct {
	ID      string
	Payload string
}

// Store 是事务发件箱存储，可被并发调用。
type Store struct {
	mu         sync.Mutex
	txs        map[string]*transaction
	pending    []*storedMessage // 已提交未标记，按 (commitSeq, writeSeq) 有序
	history    []*storedMessage // 全部已提交消息（含已标记），按提交序保留
	writeSeq   uint64
	commitSeq  uint64
	maxPending int
}

type transaction struct {
	name     string
	messages []*storedMessage
	state    txState
}

type txState int

const (
	txActive txState = iota
	txCommitted
	txAborted
)

type storedMessage struct {
	msg       Message
	writeSeq  uint64
	commitSeq uint64
	marked    bool
}

// NewStore 创建发件箱存储，maxPending 为待投递积压上限（<=0 表示不限）。
func NewStore(maxPending int) *Store {
	return &Store{txs: make(map[string]*transaction), maxPending: maxPending}
}

// Begin 以名字开启一个事务。
func (s *Store) Begin(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.beginLocked(name)
}

func (s *Store) beginLocked(name string) error {
	if name == "" {
		return ErrTxNotFound
	}
	if _, ok := s.txs[name]; ok {
		return ErrTxExists
	}
	s.txs[name] = &transaction{name: name, state: txActive}
	return nil
}

// Write 向活跃事务写入一条消息。
func (s *Store) Write(txName string, msg Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeLocked(txName, msg)
}

func (s *Store) writeLocked(txName string, msg Message) error {
	tx, err := s.activeTx(txName)
	if err != nil {
		return err
	}
	if msg.ID == "" || msg.Payload == "" {
		return ErrInvalidMessage
	}
	// 校验全部通过后才推进写入序，被拒绝的写入不改变任何计数器。
	s.writeSeq++
	tx.messages = append(tx.messages, &storedMessage{msg: msg, writeSeq: s.writeSeq})
	return nil
}

// Commit 提交事务：为事务内全部消息分配提交序并进入待投队列。
func (s *Store) Commit(txName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commitLocked(txName)
}

func (s *Store) commitLocked(txName string) error {
	tx, err := s.activeTx(txName)
	if err != nil {
		return err
	}
	// 先校验积压上限：被拒绝的提交不分配提交序、不改变待投队列，
	// 事务保持活跃，可稍后重试或中止。
	if s.maxPending > 0 && len(s.pending)+len(tx.messages) > s.maxPending {
		return ErrBacklogExceeded
	}
	s.commitSeq++
	for _, m := range tx.messages {
		m.commitSeq = s.commitSeq
		s.pending = append(s.pending, m)
		s.history = append(s.history, m)
	}
	tx.state = txCommitted
	tx.messages = nil
	return nil
}

// Abort 中止事务：其消息永不投递。
func (s *Store) Abort(txName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.abortLocked(txName)
}

func (s *Store) abortLocked(txName string) error {
	tx, err := s.activeTx(txName)
	if err != nil {
		return err
	}
	// 中止事务的消息从未进入待投队列，因此永不投递。
	tx.state = txAborted
	tx.messages = nil
	return nil
}

func (s *Store) activeTx(name string) (*transaction, error) {
	tx, ok := s.txs[name]
	if !ok {
		return nil, ErrTxNotFound
	}
	if tx.state != txActive {
		return nil, ErrTxNotActive
	}
	return tx, nil
}

// Pending 返回全部已提交未标记消息的快照，按 (commitSeq, writeSeq) 排序。
func (s *Store) Pending() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Message, 0, len(s.pending))
	for _, m := range s.pending {
		out = append(out, m.msg)
	}
	return out
}

// MarkDelivered 将指定消息标记为已投递并从待投队列移除。
func (s *Store) MarkDelivered(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.markLocked(id)
}

func (s *Store) markLocked(id string) {
	for i, m := range s.pending {
		if m.msg.ID == id {
			m.marked = true
			s.pending = append(s.pending[:i], s.pending[i+1:]...)
			return
		}
	}
}

// PendingLen 返回当前待投递积压长度。
func (s *Store) PendingLen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending)
}

// CommittedOrder 返回全部已提交消息（含已标记）按 (commitSeq, writeSeq)
// 排列的快照，即中继应投递的朴素参照序列；被中止事务的消息不在其中。
func (s *Store) CommittedOrder() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Message, 0, len(s.history))
	for _, m := range s.history {
		out = append(out, m.msg)
	}
	return out
}
