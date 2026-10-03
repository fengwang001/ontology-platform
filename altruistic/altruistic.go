// Package altruistic 实现利他锁（Altruistic Locking）管理器。
//
// 利他锁允许长事务提前捐赠（donate）自己已经用完的锁对象：对象捐赠后
// 不再有持有者，其他事务可立即加锁；但捐赠者以"尾流（wake）"关系约束
// 所有接触过其捐赠对象的事务。
package altruistic

import "sync"

// 拒绝原因。
var (
	ErrInvalidN           = &LockError{Kind: "invalid_n"}
	ErrUnknownTransaction = &LockError{Kind: "unknown_transaction"}
	ErrNotActive          = &LockError{Kind: "not_active"}
	ErrObjectOutOfRange   = &LockError{Kind: "object_out_of_range"}
	ErrAlreadyDonated     = &LockError{Kind: "already_donated"}
	ErrObjectHeld         = &LockError{Kind: "object_held"}
	ErrNotHeld            = &LockError{Kind: "not_held"}
	ErrWakeViolation      = &LockError{Kind: "wake_violation"}
	ErrWakeUnfinished     = &LockError{Kind: "wake_unfinished"}
)

// LockError 描述一次被拒绝调用的判定依据。
//
// Kind 为原因标识；Transaction（若有）为相关的最小事务号；
// Object（若有）为相关对象编号。
type LockError struct {
	Kind        string
	Transaction int
	Object      int
}

func (e *LockError) Error() string {
	return "altruistic: " + e.Kind
}

type txnState int

const (
	stateActive txnState = iota
	stateFinished
	stateAborted
)

// bitSet 是事务编号上的可增长位集。对象集合因 N<=64 直接用 uint64。
type bitSet struct {
	words []uint64
}

func (s *bitSet) add(i int) {
	w := i >> 6
	if w >= len(s.words) {
		s.words = append(s.words, make([]uint64, w+1-len(s.words))...)
	}
	s.words[w] |= 1 << uint(i&63)
}

func (s bitSet) has(i int) bool {
	w := i >> 6
	return w < len(s.words) && s.words[w]&(1<<uint(i&63)) != 0
}

func (s bitSet) clone() bitSet {
	return bitSet{words: append([]uint64(nil), s.words...)}
}

// each 按编号升序遍历位集中的元素。
func (s bitSet) each(fn func(i int) bool) {
	for w, x := range s.words {
		base := w << 6
		for x != 0 {
			b := base + trailingZeros64(x)
			if !fn(b) {
				return
			}
			x &= x - 1
		}
	}
}

func trailingZeros64(x uint64) int {
	n := 0
	for x&1 == 0 {
		x >>= 1
		n++
	}
	return n
}

// Manager 是利他锁管理器。零值不可用，必须用 New 构造。
type Manager struct {
	mu sync.Mutex

	n        int
	nextID   int
	active   bitSet // 全部活跃事务
	state    []txnState
	locked   []uint64   // 每个事务曾经加锁过的全部对象（含已捐赠）
	holding  []uint64   // 每个事务当前持有的对象
	donated  []uint64   // 每个事务捐赠过的全部对象（只增）
	wk       []bitSet   // 尾流集合：wk(t) 为约束 t 的捐赠事务
	donorsOf [64]bitSet // donorsOf[o]：捐赠过 o 的全部事务

	holder [64]int // 每个对象的当前持有者，-1 表示无
}

// New 创建管理 N 个对象（编号 0..N-1）的管理器。
// N 不在 1..64 时以配置非法整体拒绝。
func New(n int) (*Manager, error) {
	if n < 1 || n > 64 {
		return nil, ErrInvalidN
	}
	m := &Manager{n: n, nextID: 1}
	for o := range m.holder {
		m.holder[o] = -1
	}
	return m, nil
}

// Begin 开启新事务，返回从 1 起递增的事务号。
func (m *Manager) Begin() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	t := m.nextID
	m.nextID++
	m.state = append(m.state, stateActive)
	m.locked = append(m.locked, 0)
	m.holding = append(m.holding, 0)
	m.donated = append(m.donated, 0)
	m.wk = append(m.wk, bitSet{})
	m.active.add(t)
	return t
}

// Lock 请求事务 t 加锁对象 o。
func (m *Manager) Lock(t, o int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkActiveObject(t, o); err != nil {
		return err
	}
	i := t - 1
	objBit := uint64(1) << uint(o)

	// ① 自己捐赠过的对象不可重新加锁。
	if m.donated[i]&objBit != 0 {
		return &LockError{Kind: ErrAlreadyDonated.Kind, Transaction: t, Object: o}
	}
	// ② 自己已持有：成功且无变化。
	if m.holder[o] == t {
		return nil
	}
	// ③ 被他人持有：被占用（先于尾流判定）。
	if m.holder[o] != -1 {
		return &LockError{Kind: ErrObjectHeld.Kind, Transaction: m.holder[o], Object: o}
	}

	// ④ 尾流判定。C = wk(t) 中仍活跃的事务 ∪ 捐赠过 o 的其他活跃事务。
	sPrime := m.locked[i] | objBit
	c := m.wk[i].clone()
	m.donorsOf[o].each(func(donor int) bool {
		if donor != t && m.state[donor-1] == stateActive {
			c.add(donor)
		}
		return true
	})
	var violation int = -1
	c.each(func(u int) bool {
		if u == t || m.state[u-1] != stateActive {
			return true
		}
		if sPrime&^m.donated[u-1] != 0 {
			violation = u // 升序遍历，第一个即事务号最小者
			return false
		}
		return true
	})
	if violation != -1 {
		return &LockError{Kind: ErrWakeViolation.Kind, Transaction: violation, Object: o}
	}

	// ⑤ 加锁成功：记录持有与历史，并把捐赠过 o 的其他活跃事务并入尾流。
	m.holder[o] = t
	m.holding[i] |= objBit
	m.locked[i] = sPrime
	m.donorsOf[o].each(func(donor int) bool {
		if donor != t && m.state[donor-1] == stateActive {
			m.wk[i].add(donor)
		}
		return true
	})
	return nil
}

// Donate 请求事务 t 捐赠对象 o。
func (m *Manager) Donate(t, o int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkActiveObject(t, o); err != nil {
		return err
	}
	i := t - 1
	objBit := uint64(1) << uint(o)

	// o 须由 t 持有；捐赠不做尾流判定。
	if m.holder[o] != t {
		return &LockError{Kind: ErrNotHeld.Kind, Transaction: t, Object: o}
	}
	m.holder[o] = -1
	m.holding[i] &^= objBit
	m.donated[i] |= objBit
	m.donorsOf[o].add(t)
	return nil
}

// Finish 结束事务 t。
func (m *Manager) Finish(t int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkActive(t); err != nil {
		return err
	}
	i := t - 1

	// 尾流中仍有活跃事务则拒绝，报事务号最小者。
	smallest := -1
	m.wk[i].each(func(u int) bool {
		if m.state[u-1] == stateActive {
			smallest = u
			return false
		}
		return true
	})
	if smallest != -1 {
		return &LockError{Kind: ErrWakeUnfinished.Kind, Transaction: smallest}
	}

	m.releaseHolding(i)
	m.state[i] = stateFinished
	m.active = m.active.without(t)
	return nil
}

// Abort 级联中止事务 t，返回被中止事务的升序列表。
func (m *Manager) Abort(t int) ([]int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkActive(t); err != nil {
		return nil, err
	}

	// 闭包：A={t}，反复并入 wk 中含有 A 内某事务的活跃事务。
	a := bitSet{}
	a.add(t)
	for {
		grew := false
		m.active.each(func(u int) bool {
			if a.has(u) {
				return true
			}
			inWake := false
			a.each(func(v int) bool {
				if m.wk[u-1].has(v) {
					inWake = true
					return false
				}
				return true
			})
			if inWake {
				a.add(u)
				grew = true
			}
			return true
		})
		if !grew {
			break
		}
	}

	var result []int
	a.each(func(u int) bool {
		result = append(result, u)
		return true
	})
	for _, u := range result {
		m.releaseHolding(u - 1)
		m.state[u-1] = stateAborted
		m.active = m.active.without(u)
	}
	return result, nil
}

// checkActiveObject 按规定顺序执行公共前置校验：
// 事务号不存在 → 事务非活跃 → 对象越界。
func (m *Manager) checkActiveObject(t, o int) error {
	if err := m.checkActive(t); err != nil {
		return err
	}
	if o < 0 || o >= m.n {
		return &LockError{Kind: ErrObjectOutOfRange.Kind, Transaction: t, Object: o}
	}
	return nil
}

// checkActive 校验事务存在且处于活跃态。
func (m *Manager) checkActive(t int) error {
	if t < 1 || t > len(m.state) {
		return &LockError{Kind: ErrUnknownTransaction.Kind, Transaction: t}
	}
	if m.state[t-1] != stateActive {
		return &LockError{Kind: ErrNotActive.Kind, Transaction: t}
	}
	return nil
}

// releaseHolding 释放事务当前持有的全部对象。
func (m *Manager) releaseHolding(i int) {
	h := m.holding[i]
	for h != 0 {
		b := trailingZeros64(h)
		m.holder[b] = -1
		h &= h - 1
	}
	m.holding[i] = 0
}

// without 返回删除元素 i 后的位集副本。
func (s bitSet) without(i int) bitSet {
	out := s.clone()
	w := i >> 6
	if w < len(out.words) {
		out.words[w] &^= 1 << uint(i&63)
	}
	return out
}
