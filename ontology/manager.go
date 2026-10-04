// Package ontology 实现利他锁（Altruistic Locking）管理器。
//
// 长事务可以通过 Donate 提前捐赠已用完的对象，供其他事务加锁；
// 接触过捐赠对象的事务会被尾流（wake）规则约束：若事务 U 在某对象
// 已属于 donated(T) 的时刻加锁了该对象，则 T 进入 U 的尾流集合 wk(U)，
// 此后 U 曾经加锁过的全部对象 locked(U) 必须始终是 donated(T) 的子集，
// 且 U 在 T 结束之前不能 Finish。
package ontology

import (
	"fmt"
	"sort"
	"sync"
)

// Reason 表示一次调用被拒绝的原因类别。
type Reason int

const (
	// ReasonTxNotFound 事务号不存在。
	ReasonTxNotFound Reason = iota
	// ReasonTxNotActive 事务不是活跃态（已结束或已中止）。
	ReasonTxNotActive
	// ReasonObjectOutOfRange 对象编号越界（仅 Lock、Donate 检查）。
	ReasonObjectOutOfRange
	// ReasonAlreadyDonated 对象已被本事务捐赠，不可重新加锁。
	ReasonAlreadyDonated
	// ReasonHeldByOther 对象被其他事务持有。
	ReasonHeldByOther
	// ReasonWakeViolation 尾流违规：locked(t)∪{o} 不是某约束事务 donated 的子集。
	ReasonWakeViolation
	// ReasonNotHolder 捐赠的对象并非由本事务持有。
	ReasonNotHolder
	// ReasonWakePending 尾流未结束：wk(t) 中仍有活跃事务。
	ReasonWakePending
)

func (r Reason) String() string {
	switch r {
	case ReasonTxNotFound:
		return "tx not found"
	case ReasonTxNotActive:
		return "tx not active"
	case ReasonObjectOutOfRange:
		return "object out of range"
	case ReasonAlreadyDonated:
		return "already donated"
	case ReasonHeldByOther:
		return "held by other"
	case ReasonWakeViolation:
		return "wake violation"
	case ReasonNotHolder:
		return "not holder"
	case ReasonWakePending:
		return "wake pending"
	}
	return "unknown"
}

// RejectError 描述一次被拒绝的调用。Blocker 在尾流相关拒绝中
// 记录事务号最小的约束事务，在 ReasonHeldByOther 中记录当前持有者。
type RejectError struct {
	Reason  Reason
	Tx      int
	Object  int
	Blocker int
}

func (e *RejectError) Error() string {
	switch e.Reason {
	case ReasonHeldByOther, ReasonWakeViolation, ReasonWakePending:
		return fmt.Sprintf("tx %d object %d rejected: %s (by tx %d)", e.Tx, e.Object, e.Reason, e.Blocker)
	case ReasonObjectOutOfRange:
		return fmt.Sprintf("tx %d object %d rejected: %s", e.Tx, e.Object, e.Reason)
	default:
		return fmt.Sprintf("tx %d rejected: %s", e.Tx, e.Reason)
	}
}

type txState int

const (
	stateActive txState = iota
	stateFinished
	stateAborted
)

// transaction 记录单个事务的全部状态。对象集合以位集表示，第 o 位对应对象 o。
type transaction struct {
	id      int
	state   txState
	locked  uint64 // 曾经加锁过的全部对象（含现持有与已捐赠）
	held    uint64 // 当前持有的对象
	donated uint64 // 捐赠过的全部对象，只增不减
	wake    map[int]struct{}
}

// Manager 是利他锁管理器。所有方法均可并发调用，
// 效果等价于某个串行顺序（内部由互斥锁串行化）。
type Manager struct {
	mu     sync.Mutex
	n      int
	nextID int
	holder []int // holder[o] 为对象 o 的持有者事务号，0 表示无持有者
	txs    map[int]*transaction
}

// NewManager 构造管理器，对象编号为 0 到 n-1。
// n 不在 [1,64] 时以配置非法整体拒绝。
func NewManager(n int) (*Manager, error) {
	if n < 1 || n > 64 {
		return nil, fmt.Errorf("invalid config: object count %d out of range [1,64]", n)
	}
	return &Manager{
		n:      n,
		nextID: 1,
		holder: make([]int, n),
		txs:    make(map[int]*transaction),
	}, nil
}

// Begin 开启新事务，返回从 1 起递增的事务号。
func (m *Manager) Begin() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.nextID
	m.nextID++
	m.txs[id] = &transaction{id: id, wake: make(map[int]struct{})}
	return id
}

// activeTx 做通用判定：事务号不存在、事务不是活跃态、对象越界。
// checkObject 为 false 时跳过对象越界判定（Finish、Abort）。
// 调用方须持有 m.mu。
func (m *Manager) activeTx(t, o int, checkObject bool) (*transaction, *RejectError) {
	tx, ok := m.txs[t]
	if !ok {
		return nil, &RejectError{Reason: ReasonTxNotFound, Tx: t, Object: o}
	}
	if tx.state != stateActive {
		return nil, &RejectError{Reason: ReasonTxNotActive, Tx: t, Object: o}
	}
	if checkObject && (o < 0 || o >= m.n) {
		return nil, &RejectError{Reason: ReasonObjectOutOfRange, Tx: t, Object: o}
	}
	return tx, nil
}

// activeIDs 返回活跃事务号的升序列表。调用方须持有 m.mu。
func (m *Manager) activeIDs() []int {
	ids := make([]int, 0, len(m.txs))
	for id, tx := range m.txs {
		if tx.state == stateActive {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	return ids
}

// Lock 尝试让事务 t 加锁对象 o，依次判定，只报第一个失败。
func (m *Manager) Lock(t, o int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, rej := m.activeTx(t, o, true)
	if rej != nil {
		return rej
	}
	bit := uint64(1) << uint(o)
	// ① o 在 t 的 donated 中，以已捐赠拒绝。
	if tx.donated&bit != 0 {
		return &RejectError{Reason: ReasonAlreadyDonated, Tx: t, Object: o}
	}
	// ② o 的持有者是 t 自己则成功且无变化。
	if m.holder[o] == t {
		return nil
	}
	// ③ o 被他人持有，以被占用拒绝。
	if m.holder[o] != 0 {
		return &RejectError{Reason: ReasonHeldByOther, Tx: t, Object: o, Blocker: m.holder[o]}
	}
	// ④ 尾流判定：S' = locked(t)∪{o} 必须是 C 中每个 T 的 donated(T) 的子集，
	// C = wk(t) 中仍活跃的事务 ∪ {满足 o∈donated(T) 的其他活跃事务 T}。
	sPrime := tx.locked | bit
	for _, id := range m.activeIDs() {
		if id == t {
			continue
		}
		other := m.txs[id]
		_, inWake := tx.wake[id]
		if !inWake && other.donated&bit == 0 {
			continue
		}
		if sPrime&^other.donated != 0 {
			return &RejectError{Reason: ReasonWakeViolation, Tx: t, Object: o, Blocker: id}
		}
	}
	// ⑤ 加锁成功，并把满足 o∈donated(T) 的其他活跃事务 T 并入 wk(t)。
	m.holder[o] = t
	tx.locked |= bit
	tx.held |= bit
	for _, id := range m.activeIDs() {
		if id == t {
			continue
		}
		if m.txs[id].donated&bit != 0 {
			tx.wake[id] = struct{}{}
		}
	}
	return nil
}

// Donate 让事务 t 捐赠对象 o：o 须由 t 持有，之后 o 无持有者。
func (m *Manager) Donate(t, o int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, rej := m.activeTx(t, o, true)
	if rej != nil {
		return rej
	}
	if m.holder[o] != t {
		return &RejectError{Reason: ReasonNotHolder, Tx: t, Object: o}
	}
	bit := uint64(1) << uint(o)
	m.holder[o] = 0
	tx.held &^= bit
	tx.donated |= bit
	return nil
}

// Finish 结束事务 t：wk(t) 中仍有活跃事务时以尾流未结束拒绝，
// 否则释放全部持有并转已结束。
func (m *Manager) Finish(t int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, rej := m.activeTx(t, 0, false)
	if rej != nil {
		return rej
	}
	for _, id := range m.activeIDs() {
		if _, ok := tx.wake[id]; ok {
			return &RejectError{Reason: ReasonWakePending, Tx: t, Blocker: id}
		}
	}
	m.release(tx)
	tx.state = stateFinished
	return nil
}

// Abort 中止事务 t 并级联：反复把 wk 中含有集合 A 内某事务的
// 活跃事务并入 A，A 中全部转已中止并释放持有，返回 A 的升序列表。
func (m *Manager) Abort(t int) ([]int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, rej := m.activeTx(t, 0, false)
	if rej != nil {
		return nil, rej
	}
	inA := map[int]bool{t: true}
	for changed := true; changed; {
		changed = false
		for _, id := range m.activeIDs() {
			if inA[id] {
				continue
			}
			for w := range m.txs[id].wake {
				if inA[w] {
					inA[id] = true
					changed = true
					break
				}
			}
		}
	}
	out := make([]int, 0, len(inA))
	for id := range inA {
		tx := m.txs[id]
		m.release(tx)
		tx.state = stateAborted
		out = append(out, id)
	}
	sort.Ints(out)
	return out, nil
}

// release 释放事务当前持有的全部对象。调用方须持有 m.mu。
func (m *Manager) release(tx *transaction) {
	held := tx.held
	for o := 0; held != 0; o++ {
		if held&1 != 0 && m.holder[o] == tx.id {
			m.holder[o] = 0
		}
		held >>= 1
	}
	tx.held = 0
}
