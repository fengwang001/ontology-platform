// Package lockmgr 实现基于年龄的死锁预防锁管理器（wound-wait 方案）。
//
// 规则概要：事务开始时获得从 1 起连续递增的年龄号，号小者更老。
// 申请键的独占锁时：键空闲则授予；持有者是自己则视为已持有；
// 持有者更年轻则伤害（wound）持有者并夺锁；持有者更老则进入该键等待队列。
// 锁释放时，队列中年龄号最小的等待者获锁。所有等待边都从年轻指向年老，
// 因此不可能出现等待环，无需构造等待图。
package lockmgr

import (
	"fmt"
	"sort"
	"sync"
)

// TxnState 表示事务状态。
type TxnState int

const (
	// Active 事务活跃，可申请锁。
	Active TxnState = iota
	// Waiting 事务正在某个键的等待队列中。
	Waiting
	// Wounded 事务已被更老的事务伤害，只能重启。
	Wounded
	// Committed 事务已提交（终态）。
	Committed
	// Aborted 事务已中止（终态）。
	Aborted
)

func (s TxnState) String() string {
	switch s {
	case Active:
		return "Active"
	case Waiting:
		return "Waiting"
	case Wounded:
		return "Wounded"
	case Committed:
		return "Committed"
	case Aborted:
		return "Aborted"
	}
	return "Unknown"
}

// RejectReason 表示操作被拒绝的原因。
type RejectReason int

const (
	// ReasonEmptyKey 键为空。
	ReasonEmptyKey RejectReason = iota + 1
	// ReasonTxnNotFound 事务不存在。
	ReasonTxnNotFound
	// ReasonTxnFinished 事务已提交或已中止。
	ReasonTxnFinished
	// ReasonTxnWounded 事务已被伤害。
	ReasonTxnWounded
	// ReasonTxnWaiting 事务正在等待。
	ReasonTxnWaiting
	// ReasonNotWounded 重启一个并非已伤害的事务。
	ReasonNotWounded
)

func (r RejectReason) String() string {
	switch r {
	case ReasonEmptyKey:
		return "键为空"
	case ReasonTxnNotFound:
		return "事务不存在"
	case ReasonTxnFinished:
		return "事务已提交或已中止"
	case ReasonTxnWounded:
		return "事务已被伤害"
	case ReasonTxnWaiting:
		return "事务正在等待"
	case ReasonNotWounded:
		return "事务并非已伤害状态，不能重启"
	}
	return "未知原因"
}

// RejectError 是被拒绝操作返回的错误，携带固定顺序检查出的第一个原因。
type RejectError struct {
	Op     string
	TxnID  int
	Key    string
	Reason RejectReason
}

func (e *RejectError) Error() string {
	if e.Key != "" {
		return fmt.Sprintf("%s 被拒绝: txn=%d key=%q 原因=%s", e.Op, e.TxnID, e.Key, e.Reason)
	}
	return fmt.Sprintf("%s 被拒绝: txn=%d 原因=%s", e.Op, e.TxnID, e.Reason)
}

// TxnInfo 是事务状态的可查询快照。
type TxnInfo struct {
	ID         int
	Age        int
	State      TxnState
	WaitingKey string   // 仅 Waiting 时有意义
	WoundedBy  int      // 仅 Wounded 时有意义：伤害者的年龄号
	HeldKeys   []string // 当前持有的键（排序后）
}

// WaiterInfo 描述键等待队列中的一个等待者。
type WaiterInfo struct {
	TxnID int
	Age   int
}

// KeyInfo 是一个键的持有者与等待队列快照。
type KeyInfo struct {
	Key       string
	Holder    int // 持有者事务 ID，0 表示空闲
	HolderAge int // 持有者年龄号，空闲时为 0
	Queue     []WaiterInfo
}

type txn struct {
	id         int
	age        int
	state      TxnState
	waitingKey string
	woundedBy  int
	held       map[string]bool
}

type keyLock struct {
	key    string
	holder int // txn id，0 表示空闲
	queue  []*txn
}

// Manager 是基于年龄的锁管理器，所有方法均可并发调用。
type Manager struct {
	mu      sync.Mutex
	nextAge int
	txns    map[int]*txn
	locks   map[string]*keyLock
}

// NewManager 创建一个空的锁管理器。
func NewManager() *Manager {
	return &Manager{
		txns:  make(map[int]*txn),
		locks: make(map[string]*keyLock),
	}
}

// Begin 开始一个事务，分配从 1 起连续递增的年龄号，返回事务 ID。
func (m *Manager) Begin() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextAge++
	id := m.nextAge
	m.txns[id] = &txn{id: id, age: id, state: Active, held: make(map[string]bool)}
	return id
}

// Acquire 为事务申请键的独占锁。
//
// 非法情形按固定顺序只报第一个：键为空、事务不存在、事务已提交或已中止、
// 事务已被伤害、事务正在等待。被拒绝时不改变任何状态。
func (m *Manager) Acquire(id int, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if key == "" {
		return &RejectError{Op: "Acquire", TxnID: id, Key: key, Reason: ReasonEmptyKey}
	}
	t, ok := m.txns[id]
	if !ok {
		return &RejectError{Op: "Acquire", TxnID: id, Key: key, Reason: ReasonTxnNotFound}
	}
	if t.state == Committed || t.state == Aborted {
		return &RejectError{Op: "Acquire", TxnID: id, Key: key, Reason: ReasonTxnFinished}
	}
	if t.state == Wounded {
		return &RejectError{Op: "Acquire", TxnID: id, Key: key, Reason: ReasonTxnWounded}
	}
	if t.state == Waiting {
		return &RejectError{Op: "Acquire", TxnID: id, Key: key, Reason: ReasonTxnWaiting}
	}

	kl := m.keyLocked(key)
	switch {
	case kl.holder == id:
		// 持有者就是自己：视为已持有，不改变任何状态。
		return nil
	case kl.holder == 0:
		m.grantLocked(kl, t)
		return nil
	}

	holder := m.txns[kl.holder]
	if holder.age > t.age {
		// 持有者更年轻：伤害持有者，随后本键授予申请者。
		delete(holder.held, key)
		kl.holder = 0
		m.woundLocked(holder, t.age)
		m.grantLocked(kl, t)
		return nil
	}

	// 持有者更老：申请者进入该键等待队列。
	t.state = Waiting
	t.waitingKey = key
	kl.queue = append(kl.queue, t)
	return nil
}

// Commit 提交事务并释放其全部锁。要求事务处于活跃状态。
func (m *Manager) Commit(id int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := m.txns[id]
	if !ok {
		return &RejectError{Op: "Commit", TxnID: id, Reason: ReasonTxnNotFound}
	}
	if t.state == Committed || t.state == Aborted {
		return &RejectError{Op: "Commit", TxnID: id, Reason: ReasonTxnFinished}
	}
	if t.state == Wounded {
		return &RejectError{Op: "Commit", TxnID: id, Reason: ReasonTxnWounded}
	}
	if t.state == Waiting {
		return &RejectError{Op: "Commit", TxnID: id, Reason: ReasonTxnWaiting}
	}

	m.releaseAllLocked(t)
	t.state = Committed
	return nil
}

// Abort 中止事务。活跃、等待、已伤害状态都允许，释放全部锁与排队请求。
func (m *Manager) Abort(id int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := m.txns[id]
	if !ok {
		return &RejectError{Op: "Abort", TxnID: id, Reason: ReasonTxnNotFound}
	}
	if t.state == Committed || t.state == Aborted {
		return &RejectError{Op: "Abort", TxnID: id, Reason: ReasonTxnFinished}
	}

	m.releaseAllLocked(t)
	t.state = Aborted
	return nil
}

// Restart 重启一个已伤害的事务：回到活跃、年龄号不变、不持有任何锁。
// 重启一个并非已伤害的事务会被拒绝。
func (m *Manager) Restart(id int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := m.txns[id]
	if !ok {
		return &RejectError{Op: "Restart", TxnID: id, Reason: ReasonTxnNotFound}
	}
	if t.state != Wounded {
		return &RejectError{Op: "Restart", TxnID: id, Reason: ReasonNotWounded}
	}
	t.state = Active
	t.woundedBy = 0
	return nil
}

// TxnInfo 查询事务状态快照。
func (m *Manager) TxnInfo(id int) (TxnInfo, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.txns[id]
	if !ok {
		return TxnInfo{}, false
	}
	return txnInfoOf(t), true
}

// TxnInfos 返回全部事务的快照（按事务 ID 排序），用于不变量自检。
func (m *Manager) TxnInfos() []TxnInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]int, 0, len(m.txns))
	for id := range m.txns {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	infos := make([]TxnInfo, 0, len(ids))
	for _, id := range ids {
		infos = append(infos, txnInfoOf(m.txns[id]))
	}
	return infos
}

func txnInfoOf(t *txn) TxnInfo {
	info := TxnInfo{
		ID:         t.id,
		Age:        t.age,
		State:      t.state,
		WaitingKey: t.waitingKey,
		WoundedBy:  t.woundedBy,
	}
	for k := range t.held {
		info.HeldKeys = append(info.HeldKeys, k)
	}
	sort.Strings(info.HeldKeys)
	return info
}

// KeyInfo 查询一个键的持有者与等待队列快照。
func (m *Manager) KeyInfo(key string) KeyInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.keyInfoLocked(key)
}

// KeyInfos 返回所有键的快照（按键名排序），用于不变量自检。
func (m *Manager) KeyInfos() []KeyInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	keys := make([]string, 0, len(m.locks))
	for k := range m.locks {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	infos := make([]KeyInfo, 0, len(keys))
	for _, k := range keys {
		infos = append(infos, m.keyInfoLocked(k))
	}
	return infos
}

// Snapshot 在同一把锁下返回全部事务与全部键的一致性快照，用于并发下的不变量自检。
func (m *Manager) Snapshot() ([]TxnInfo, []KeyInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]int, 0, len(m.txns))
	for id := range m.txns {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	txns := make([]TxnInfo, 0, len(ids))
	for _, id := range ids {
		txns = append(txns, txnInfoOf(m.txns[id]))
	}
	keys := make([]string, 0, len(m.locks))
	for k := range m.locks {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	infos := make([]KeyInfo, 0, len(keys))
	for _, k := range keys {
		infos = append(infos, m.keyInfoLocked(k))
	}
	return txns, infos
}

func (m *Manager) keyInfoLocked(key string) KeyInfo {
	info := KeyInfo{Key: key}
	kl, ok := m.locks[key]
	if !ok {
		return info
	}
	info.Holder = kl.holder
	if kl.holder != 0 {
		info.HolderAge = m.txns[kl.holder].age
	}
	for _, w := range kl.queue {
		info.Queue = append(info.Queue, WaiterInfo{TxnID: w.id, Age: w.age})
	}
	return info
}

// keyLocked 返回键对应的锁结构，不存在则创建。
func (m *Manager) keyLocked(key string) *keyLock {
	kl, ok := m.locks[key]
	if !ok {
		kl = &keyLock{key: key}
		m.locks[key] = kl
	}
	return kl
}

// grantLocked 把键授予事务（调用方需保证键空闲且事务活跃）。
func (m *Manager) grantLocked(kl *keyLock, t *txn) {
	kl.holder = t.id
	t.held[kl.key] = true
}

// releaseKeyLocked 释放一个键，并把键授予队列中年龄号最小的等待者。
func (m *Manager) releaseKeyLocked(kl *keyLock) {
	kl.holder = 0
	if len(kl.queue) == 0 {
		return
	}
	best := 0
	for i, w := range kl.queue {
		if w.age < kl.queue[best].age {
			best = i
		}
	}
	t := kl.queue[best]
	kl.queue = append(kl.queue[:best], kl.queue[best+1:]...)
	t.state = Active
	t.waitingKey = ""
	m.grantLocked(kl, t)
}

// releaseAllLocked 释放事务持有的全部锁，并撤销其在任何键上的排队请求。
func (m *Manager) releaseAllLocked(t *txn) {
	keys := make([]string, 0, len(t.held))
	for k := range t.held {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		delete(t.held, k)
		m.releaseKeyLocked(m.locks[k])
	}
	if t.state == Waiting {
		kl := m.locks[t.waitingKey]
		for i, w := range kl.queue {
			if w.id == t.id {
				kl.queue = append(kl.queue[:i], kl.queue[i+1:]...)
				break
			}
		}
		t.waitingKey = ""
	}
}

// woundLocked 伤害一个事务：释放其全部锁、撤销其排队请求、状态变为已伤害。
// 调用方需已把当前要夺取的键从 t.held 中摘除。
func (m *Manager) woundLocked(t *txn, byAge int) {
	m.releaseAllLocked(t)
	t.state = Wounded
	t.woundedBy = byAge
}
