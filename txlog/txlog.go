// Package txlog 实现事务半消息的回查与提交日志。
//
// 半消息发送后处于待定状态，对消费者不可见且不占日志位点；
// 只有在提交时才追加到日志末尾并获得连续位点。
// 待定消息到期后由 Tick 触发回查回调，依据回调返回值或
// 回查次数上限决定其终态（已提交 / 已回滚）。
package txlog

import (
	"errors"
	"fmt"
	"sync"
)

// State 表示一条事务消息的生命周期状态。
type State int

const (
	// StatePending 待定：已发送但尚未提交或回滚，对消费者不可见。
	StatePending State = iota
	// StateCommitted 已提交：已追加到日志并获得位点，对消费者可见。
	StateCommitted
	// StateRolledBack 已回滚：被显式回滚或因回查耗尽可能性而回滚。
	StateRolledBack
)

func (s State) String() string {
	switch s {
	case StatePending:
		return "pending"
	case StateCommitted:
		return "committed"
	case StateRolledBack:
		return "rolled_back"
	default:
		return "unknown"
	}
}

// Decision 是回查回调的返回决策。
type Decision int

const (
	// DecisionUnknown 表示暂时无法判定，消息继续等待下次回查。
	DecisionUnknown Decision = iota
	// DecisionCommit 表示立即提交该消息。
	DecisionCommit
	// DecisionRollback 表示立即回滚该消息。
	DecisionRollback
)

// RollbackReason 区分消息进入已回滚状态的原因。
type RollbackReason string

const (
	// ReasonNone 表示未回滚。
	ReasonNone RollbackReason = ""
	// ReasonExplicit 表示由显式回滚调用或回调返回回滚导致。
	ReasonExplicit RollbackReason = "explicit"
	// ReasonCheckExhausted 表示第 M 次回查仍返回未知，回查耗尽。
	ReasonCheckExhausted RollbackReason = "check_exhausted"
)

// 可区分的拒绝原因，调用方可用 errors.Is 判定。
var (
	// ErrUnknownTx 事务标识不存在。
	ErrUnknownTx = errors.New("txlog: unknown transaction id")
	// ErrDuplicateTx 事务标识重复发送（终态后标识仍保留）。
	ErrDuplicateTx = errors.New("txlog: duplicate transaction id")
	// ErrAlreadyCommitted 对已提交的消息再次提交或回滚。
	ErrAlreadyCommitted = errors.New("txlog: transaction already committed")
	// ErrAlreadyRolledBack 对已回滚的消息再次提交或回滚。
	ErrAlreadyRolledBack = errors.New("txlog: transaction already rolled back")
	// ErrClockBackward 传入的 now 小于此前任一次调用传入的 now。
	ErrClockBackward = errors.New("txlog: clock moved backward")
	// ErrInvalidParam 构造参数不满足 F>=0、I>=1、M>=1。
	ErrInvalidParam = errors.New("txlog: invalid parameter")
)

// Entry 是提交日志中的一条记录，位点从 0 起连续无空洞。
type Entry struct {
	Offset  int
	TxID    string
	Payload string
}

// Record 是一条事务消息的只读快照。
type Record struct {
	TxID           string
	Payload        string
	State          State
	Offset         int // 仅已提交有效，否则为 -1
	CreatedAt      int64
	NextCheckAt    int64 // 仅待定有效：下次可被回查的时刻
	CheckCount     int   // 回查回调已被调用的次数
	RollbackReason RollbackReason
}

// CheckCallback 是回查回调，在不持内部锁的情况下被调用，
// 回调内允许调用 Commit 与 Rollback。
type CheckCallback func(txID string, payload string) Decision

// message 是内部可变状态。
type message struct {
	txID           string
	payload        string
	state          State
	offset         int
	createdAt      int64
	nextCheckAt    int64
	checkCount     int
	rollbackReason RollbackReason
}

// Log 是事务半消息的提交日志与回查调度器，可并发使用。
type Log struct {
	mu      sync.Mutex
	f       int64
	i       int64
	m       int
	maxNow  int64
	hasNow  bool
	msgs    map[string]*message
	order   []*message // 按发送（创建）先后
	entries []Entry    // 提交日志，位点连续
}

// New 构造日志。要求 f>=0、i>=1、m>=1，否则返回 ErrInvalidParam。
func New(f, i int64, m int) (*Log, error) {
	if f < 0 || i < 1 || m < 1 {
		return nil, fmt.Errorf("%w: require f>=0, i>=1, m>=1, got f=%d i=%d m=%d", ErrInvalidParam, f, i, m)
	}
	return &Log{
		f:    f,
		i:    i,
		m:    m,
		msgs: make(map[string]*message),
	}, nil
}

// checkClock 校验调用方时钟不倒退，并在接受后推进最大时刻。
// 调用时必须持有锁；被拒绝时不改变任何状态。
func (l *Log) checkClock(now int64) error {
	if l.hasNow && now < l.maxNow {
		return fmt.Errorf("%w: now=%d < last=%d", ErrClockBackward, now, l.maxNow)
	}
	l.maxNow = now
	l.hasNow = true
	return nil
}

// Send 发送半消息：状态为待定，对消费者不可见，不占日志位点。
// now 为调用方时钟；事务标识重复（含终态后）或时钟倒退时整体拒绝。
func (l *Log) Send(txID, payload string, now int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.checkClock(now); err != nil {
		return err
	}
	if _, ok := l.msgs[txID]; ok {
		return fmt.Errorf("%w: %q", ErrDuplicateTx, txID)
	}
	msg := &message{
		txID:        txID,
		payload:     payload,
		state:       StatePending,
		offset:      -1,
		createdAt:   now,
		nextCheckAt: now + l.f,
	}
	l.msgs[txID] = msg
	l.order = append(l.order, msg)
	return nil
}

// commitLocked 在持锁状态下把待定消息追加到日志末尾并置为已提交。
func (l *Log) commitLocked(msg *message) {
	msg.state = StateCommitted
	msg.offset = len(l.entries)
	l.entries = append(l.entries, Entry{
		Offset:  msg.offset,
		TxID:    msg.txID,
		Payload: msg.payload,
	})
}

// rollbackLocked 在持锁状态下把待定消息置为已回滚并记录原因。
func (l *Log) rollbackLocked(msg *message, reason RollbackReason) {
	msg.state = StateRolledBack
	msg.rollbackReason = reason
}

// Commit 提交待定消息：此刻才追加到日志末尾获得位点。
// 返回分配到的位点。未知标识、已提交、已回滚分别返回可区分错误。
func (l *Log) Commit(txID string) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	msg, ok := l.msgs[txID]
	if !ok {
		return -1, fmt.Errorf("%w: %q", ErrUnknownTx, txID)
	}
	switch msg.state {
	case StateCommitted:
		return -1, fmt.Errorf("%w: %q", ErrAlreadyCommitted, txID)
	case StateRolledBack:
		return -1, fmt.Errorf("%w: %q", ErrAlreadyRolledBack, txID)
	}
	l.commitLocked(msg)
	return msg.offset, nil
}

// Rollback 回滚待定消息。未知标识、已提交、已回滚分别返回可区分错误。
func (l *Log) Rollback(txID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	msg, ok := l.msgs[txID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownTx, txID)
	}
	switch msg.state {
	case StateCommitted:
		return fmt.Errorf("%w: %q", ErrAlreadyCommitted, txID)
	case StateRolledBack:
		return fmt.Errorf("%w: %q", ErrAlreadyRolledBack, txID)
	}
	l.rollbackLocked(msg, ReasonExplicit)
	return nil
}

// Tick 推进回查调度。到点集合在推进开始时按创建先后确定：
// 待定且 nextCheckAt<=now 的消息各回查一次，每条每次推进至多一次。
// 推进期间新发送的消息不在本次处理；轮到某条时若已进入终态则跳过
// 且不计回查次数。回调在不持内部锁时调用；回调返回时若该消息
// 已进入终态（例如回调内先行提交），忽略其返回值。
func (l *Log) Tick(now int64, cb CheckCallback) error {
	l.mu.Lock()
	if err := l.checkClock(now); err != nil {
		l.mu.Unlock()
		return err
	}
	due := make([]*message, 0, len(l.order))
	for _, msg := range l.order {
		if msg.state == StatePending && msg.nextCheckAt <= now {
			due = append(due, msg)
		}
	}
	l.mu.Unlock()

	for _, msg := range due {
		l.mu.Lock()
		if msg.state != StatePending {
			l.mu.Unlock()
			continue
		}
		msg.checkCount++
		count := msg.checkCount
		l.mu.Unlock()

		decision := cb(msg.txID, msg.payload)

		l.mu.Lock()
		if msg.state != StatePending {
			// 回调内已通过 Commit/Rollback 进入终态，忽略返回值。
			l.mu.Unlock()
			continue
		}
		switch decision {
		case DecisionCommit:
			l.commitLocked(msg)
		case DecisionRollback:
			l.rollbackLocked(msg, ReasonExplicit)
		default:
			if count >= l.m {
				l.rollbackLocked(msg, ReasonCheckExhausted)
			} else {
				msg.nextCheckAt = now + l.i
			}
		}
		l.mu.Unlock()
	}
	return nil
}

// Entries 返回提交日志快照，位点从 0 起连续无空洞，只含已提交消息。
func (l *Log) Entries() []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Entry, len(l.entries))
	copy(out, l.entries)
	return out
}

// Get 返回指定事务标识的消息快照；标识不存在时 ok 为 false。
func (l *Log) Get(txID string) (rec Record, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	msg, ok := l.msgs[txID]
	if !ok {
		return Record{}, false
	}
	return Record{
		TxID:           msg.txID,
		Payload:        msg.payload,
		State:          msg.state,
		Offset:         msg.offset,
		CreatedAt:      msg.createdAt,
		NextCheckAt:    msg.nextCheckAt,
		CheckCount:     msg.checkCount,
		RollbackReason: msg.rollbackReason,
	}, true
}
