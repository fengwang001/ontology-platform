// Package lockmgr 实现基于年龄的死锁预防锁管理器（wound-wait 方案）。
//
// 规则概要：事务开始时获得从 1 起连续递增的年龄号，号小者更老。
// 老事务申请被年轻事务持有的键时伤害（wound）持有者；年轻事务申请
// 被老事务持有的键时进入等待队列。所有等待边都从年轻指向年老，
// 年龄严格偏序，因此不可能出现等待环，无需构造等待图。
package lockmgr

import (
	"errors"
	"sort"
	"sync"
)

// State 事务状态。
type State int

const (
	StateActive    State = iota // 活跃
	StateWaiting                // 等待某键
	StateWounded                // 已伤害
	StateCommitted              // 已提交
	StateAborted                // 已中止
)

// String 返回状态的中文描述。
func (s State) String() string {
	switch s {
	case StateActive:
		return "活跃"
	case StateWaiting:
		return "等待"
	case StateWounded:
		return "已伤害"
	case StateCommitted:
		return "已提交"
	case StateAborted:
		return "已中止"
	}
	return "未知"
}

// 操作被拒绝的原因。申请与提交按固定顺序只报第一个：
// 键为空、事务不存在、事务已提交或已中止、事务已被伤害、事务正在等待。
var (
	ErrEmptyKey     = errors.New("键为空")
	ErrTxNotFound   = errors.New("事务不存在")
	ErrTxTerminated = errors.New("事务已提交或已中止")
	ErrTxWounded    = errors.New("事务已被伤害")
	ErrTxWaiting    = errors.New("事务正在等待")
	// ErrTxNotWounded 重启一个并非已伤害的事务。
	ErrTxNotWounded = errors.New("事务并非已伤害状态，不能重启")
)

// TxStatus 事务状态快照。
type TxStatus struct {
	ID        uint64 // 事务 ID（等于年龄号）
	Age       uint64 // 年龄号，小者更老
	State     State
	WaitingOn string // StateWaiting 时所等的键
	WoundedBy uint64 // StateWounded 时伤害者的年龄号
}

// KeyInfo 键的持有者与等待队列快照。
type KeyInfo struct {
	Key    string
	Holder uint64   // 持有者事务 ID，0 表示空闲
	Queue  []uint64 // 等待者事务 ID，按年龄号升序
}

// Snapshot 管理器某一时刻的一致性快照。
type Snapshot struct {
	Txs  []TxStatus // 按年龄号升序
	Keys []KeyInfo  // 按键名升序
}

// Manager 是基于年龄的锁管理器，所有方法均可并发调用。
type Manager struct {
	mu      sync.Mutex
	nextAge uint64
	txs     map[uint64]*tx
	keys    map[string]*keyState
}

type tx struct {
	id        uint64
	age       uint64
	state     State
	locks     map[string]bool
	waitingOn string
	woundedBy uint64
}

type keyState struct {
	holder uint64   // 0 表示空闲
	queue  []uint64 // 等待者，按到达先后排列
}

// NewManager 创建空的锁管理器。
func NewManager() *Manager {
	return &Manager{
		txs:  make(map[uint64]*tx),
		keys: make(map[string]*keyState),
	}
}

// Begin 开启事务，返回事务 ID（即年龄号，从 1 起连续递增）。
func (m *Manager) Begin() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextAge++
	id := m.nextAge
	m.txs[id] = &tx{id: id, age: id, state: StateActive, locks: make(map[string]bool)}
	return id
}

// Acquire 申请键的独占锁。
//
// 非法情形按固定顺序只报第一个：键为空、事务不存在、事务已提交或
// 已中止、事务已被伤害、事务正在等待。被拒绝时不改变任何状态。
func (m *Manager) Acquire(txID uint64, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if key == "" {
		return ErrEmptyKey
	}
	t, err := m.requireActive(txID)
	if err != nil {
		return err
	}
	ks := m.keys[key]
	if ks == nil {
		ks = &keyState{}
		m.keys[key] = ks
	}
	switch {
	case ks.holder == 0:
		// 键空闲，直接授予。
		m.grantLocked(ks, t, key)
	case ks.holder == txID:
		// 持有者就是自己，视为已持有，不改变任何状态。
		return nil
	default:
		holder := m.txs[ks.holder]
		if t.age < holder.age {
			// 申请者更老：伤害年轻持有者，随后本键授予申请者。
			m.woundLocked(holder, t.age)
			m.grantLocked(ks, t, key)
		} else {
			// 持有者更老：申请者进入该键的等待队列。
			t.state = StateWaiting
			t.waitingOn = key
			ks.queue = append(ks.queue, txID)
		}
	}
	return nil
}

// Commit 提交事务并释放全部锁，要求事务活跃。
// 非法情形与 Acquire 相同的固定顺序只报第一个（键为空除外）。
func (m *Manager) Commit(txID uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.requireActive(txID)
	if err != nil {
		return err
	}
	m.releaseAllLocked(t)
	t.state = StateCommitted
	return nil
}

// Abort 中止事务，活跃、等待、已伤害时都允许，释放全部锁与排队请求。
func (m *Manager) Abort(txID uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.txs[txID]
	if !ok {
		return ErrTxNotFound
	}
	if t.state == StateCommitted || t.state == StateAborted {
		return ErrTxTerminated
	}
	m.cancelWaitingLocked(t)
	m.releaseAllLocked(t)
	t.woundedBy = 0
	t.state = StateAborted
	return nil
}

// Restart 重启已伤害的事务：回到活跃、年龄号不变、不持有任何锁。
func (m *Manager) Restart(txID uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.txs[txID]
	if !ok {
		return ErrTxNotFound
	}
	if t.state != StateWounded {
		return ErrTxNotWounded
	}
	t.woundedBy = 0
	t.state = StateActive
	return nil
}

// Status 查询事务状态。
func (m *Manager) Status(txID uint64) (TxStatus, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.txs[txID]
	if !ok {
		return TxStatus{}, false
	}
	return statusOf(t), true
}

// KeyInfoOf 查询键的持有者与等待队列（按年龄号升序）。
func (m *Manager) KeyInfoOf(key string) (KeyInfo, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ks, ok := m.keys[key]
	if !ok {
		return KeyInfo{}, false
	}
	return m.keyInfoLocked(key, ks), true
}

// Snapshot 返回管理器的一致性快照，供自检与测试使用。
func (m *Manager) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	snap := Snapshot{
		Txs:  make([]TxStatus, 0, len(m.txs)),
		Keys: make([]KeyInfo, 0, len(m.keys)),
	}
	for _, t := range m.txs {
		snap.Txs = append(snap.Txs, statusOf(t))
	}
	sort.Slice(snap.Txs, func(i, j int) bool { return snap.Txs[i].Age < snap.Txs[j].Age })
	names := make([]string, 0, len(m.keys))
	for name := range m.keys {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		snap.Keys = append(snap.Keys, m.keyInfoLocked(name, m.keys[name]))
	}
	return snap
}

// requireActive 按固定顺序校验事务可执行申请/提交：不存在、已终结、
// 已伤害、正在等待，只报第一个原因。
func (m *Manager) requireActive(txID uint64) (*tx, error) {
	t, ok := m.txs[txID]
	if !ok {
		return nil, ErrTxNotFound
	}
	switch t.state {
	case StateCommitted, StateAborted:
		return nil, ErrTxTerminated
	case StateWounded:
		return nil, ErrTxWounded
	case StateWaiting:
		return nil, ErrTxWaiting
	}
	return t, nil
}

// grantLocked 把键授予事务，事务回到活跃。
func (m *Manager) grantLocked(ks *keyState, t *tx, key string) {
	ks.holder = t.id
	t.locks[key] = true
	t.state = StateActive
	t.waitingOn = ""
}

// woundLocked 伤害事务：释放其全部锁、撤销其在任何键上的排队请求，
// 状态变为已伤害并记录伤害者年龄号。
func (m *Manager) woundLocked(t *tx, byAge uint64) {
	m.cancelWaitingLocked(t)
	m.releaseAllLocked(t)
	t.woundedBy = byAge
	t.state = StateWounded
}

// cancelWaitingLocked 撤销事务在任何键上的排队请求。
func (m *Manager) cancelWaitingLocked(t *tx) {
	if t.state != StateWaiting {
		return
	}
	if ks, ok := m.keys[t.waitingOn]; ok {
		ks.queue = removeTx(ks.queue, t.id)
	}
	t.waitingOn = ""
}

// releaseAllLocked 释放事务持有的全部锁（按键名排序，保证重放确定）。
// 每个被释放的键授予队列中年龄号最小的等待者，其余继续等待。
func (m *Manager) releaseAllLocked(t *tx) {
	names := make([]string, 0, len(t.locks))
	for name := range t.locks {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		delete(t.locks, name)
		ks := m.keys[name]
		ks.holder = 0
		if len(ks.queue) == 0 {
			continue
		}
		// 选出年龄号最小的等待者。
		best := 0
		for i, id := range ks.queue {
			if m.txs[id].age < m.txs[ks.queue[best]].age {
				best = i
			}
		}
		winner := m.txs[ks.queue[best]]
		ks.queue = append(ks.queue[:best], ks.queue[best+1:]...)
		m.grantLocked(ks, winner, name)
	}
}

func (m *Manager) keyInfoLocked(key string, ks *keyState) KeyInfo {
	info := KeyInfo{Key: key, Holder: ks.holder, Queue: make([]uint64, len(ks.queue))}
	copy(info.Queue, ks.queue)
	sort.Slice(info.Queue, func(i, j int) bool {
		return m.txs[info.Queue[i]].age < m.txs[info.Queue[j]].age
	})
	return info
}

func statusOf(t *tx) TxStatus {
	return TxStatus{
		ID:        t.id,
		Age:       t.age,
		State:     t.state,
		WaitingOn: t.waitingOn,
		WoundedBy: t.woundedBy,
	}
}

func removeTx(queue []uint64, id uint64) []uint64 {
	for i, v := range queue {
		if v == id {
			return append(queue[:i], queue[i+1:]...)
		}
	}
	return queue
}
