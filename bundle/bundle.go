// Package bundle 提供流表批量消息的全有或全无提交。
//
// Begin 开启批，Append 缓存消息（不带时刻），Commit 在工作副本上
// 先落地到期、再按追加次序逐条执行：任一条被拒绝则整批拒绝，
// 流表、序号、时钟与事件均不变，批保持打开可再次提交；全部成功
// 则一次性生效并销毁批。
package bundle

import (
	"errors"
	"fmt"
	"sync"

	"ontology/flowtable"
	"ontology/match"
)

// ErrBundleNotFound 表示批号不存在。
var ErrBundleNotFound = errors.New("bundle: bundle not found")

// MaxMessages 是单个批最多缓存的消息条数。
const MaxMessages = 256

// Kind 是批量消息的类型。
type Kind int

const (
	AddMsg Kind = iota
	ModifyMsg
	DeleteMsg
)

// Message 是一条批量消息（不带时刻，时刻由 Commit 指定）。
type Message struct {
	Kind         Kind
	Match        match.Match
	Prio         uint32
	CheckOverlap bool
	Action       uint32
	Importance   uint32
	Idle         uint32
	Hard         uint32
	Strict       bool
}

// NewAddMessage 构造一条 Add 消息。
func NewAddMessage(m match.Match, prio uint32, checkOverlap bool, action, importance, idle, hard uint32) Message {
	return Message{Kind: AddMsg, Match: m, Prio: prio, CheckOverlap: checkOverlap,
		Action: action, Importance: importance, Idle: idle, Hard: hard}
}

// NewModifyMessage 构造一条 Modify 消息。
func NewModifyMessage(m match.Match, prio uint32, strict bool, action uint32) Message {
	return Message{Kind: ModifyMsg, Match: m, Prio: prio, Strict: strict, Action: action}
}

// NewDeleteMessage 构造一条 Delete 消息。
func NewDeleteMessage(m match.Match, prio uint32, strict bool) Message {
	return Message{Kind: DeleteMsg, Match: m, Prio: prio, Strict: strict}
}

// validate 校验消息参数（不含时刻）。
func (msg Message) validate() error {
	switch msg.Kind {
	case AddMsg, ModifyMsg, DeleteMsg:
	default:
		return fmt.Errorf("%w: unknown message kind %d", flowtable.ErrInvalidParam, msg.Kind)
	}
	if !msg.Match.Valid() {
		return fmt.Errorf("%w: match value outside mask", flowtable.ErrInvalidParam)
	}
	if msg.Prio > 65535 {
		return fmt.Errorf("%w: prio %d out of [0,65535]", flowtable.ErrInvalidParam, msg.Prio)
	}
	if msg.Kind == AddMsg {
		if msg.Importance > 65535 {
			return fmt.Errorf("%w: importance %d out of [0,65535]", flowtable.ErrInvalidParam, msg.Importance)
		}
		if msg.Idle > 1_000_000_000 || msg.Hard > 1_000_000_000 {
			return fmt.Errorf("%w: timeout out of [0,1e9]", flowtable.ErrInvalidParam)
		}
	}
	return nil
}

// RejectError 报告整批拒绝：Index 是被拒绝消息的下标，Err 是原因。
type RejectError struct {
	Index int
	Err   error
}

func (e *RejectError) Error() string {
	return fmt.Sprintf("bundle: message %d rejected: %v", e.Index, e.Err)
}

func (e *RejectError) Unwrap() error { return e.Err }

// batch 是一个打开的批。
type batch struct {
	msgs []Message
}

// Manager 管理一批未决的批量提交。可并发使用。
type Manager struct {
	ft   *flowtable.Table
	mu   sync.Mutex
	next uint64
	open map[uint64]*batch
}

// NewManager 创建作用于 ft 的批量管理器。
func NewManager(ft *flowtable.Table) *Manager {
	return &Manager{ft: ft, next: 1, open: make(map[uint64]*batch)}
}

// Begin 开启一个批，返回批号。
func (m *Manager) Begin() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.next
	m.next++
	m.open[id] = &batch{}
	return id
}

// Append 缓存一条消息；超出 256 条或消息参数非法报参数非法。
func (m *Manager) Append(id uint64, msg Message) error {
	if err := msg.validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	b := m.open[id]
	if b == nil {
		return ErrBundleNotFound
	}
	if len(b.msgs) >= MaxMessages {
		return fmt.Errorf("%w: bundle exceeds %d messages", flowtable.ErrInvalidParam, MaxMessages)
	}
	b.msgs = append(b.msgs, msg)
	return nil
}

// Discard 丢弃一个批。
func (m *Manager) Discard(id uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.open[id] == nil {
		return ErrBundleNotFound
	}
	delete(m.open, id)
	return nil
}

// Commit 以 now 为时刻提交批：先落地到期，再按追加次序逐条执行，
// 后面的消息看得见前面的效果。全部成功则一次性生效并销毁批；
// 任一条被拒绝则整批拒绝（*RejectError），状态不变，批保持打开。
func (m *Manager) Commit(id uint64, now uint64) ([]flowtable.Event, error) {
	return m.ft.Transact(now, func(w *flowtable.Table) ([]flowtable.Event, error) {
		m.mu.Lock()
		b := m.open[id]
		m.mu.Unlock()
		if b == nil {
			return nil, ErrBundleNotFound
		}
		var all []flowtable.Event
		evs, err := w.Advance(now)
		if err != nil {
			return nil, err
		}
		all = append(all, evs...)
		for i, msg := range b.msgs {
			evs, err := exec(w, msg, now)
			if err != nil {
				return nil, &RejectError{Index: i, Err: err}
			}
			all = append(all, evs...)
		}
		m.mu.Lock()
		delete(m.open, id)
		m.mu.Unlock()
		return all, nil
	})
}

// exec 在工作副本上执行一条消息。
func exec(w *flowtable.Table, msg Message, now uint64) ([]flowtable.Event, error) {
	switch msg.Kind {
	case AddMsg:
		_, evs, err := w.Add(msg.Match, msg.Prio, msg.CheckOverlap, msg.Action, msg.Importance, msg.Idle, msg.Hard, now)
		return evs, err
	case ModifyMsg:
		_, evs, err := w.Modify(msg.Match, msg.Prio, msg.Strict, msg.Action, now)
		return evs, err
	case DeleteMsg:
		_, evs, err := w.Delete(msg.Match, msg.Prio, msg.Strict, now)
		return evs, err
	}
	return nil, fmt.Errorf("%w: unknown message kind %d", flowtable.ErrInvalidParam, msg.Kind)
}
