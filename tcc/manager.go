package tcc

import (
	"sync"

	"ontology/ledger"
)

const (
	MaxTTL = 1_000_000_000
	MaxCap = 1_000_000
	MaxNow = 100_000_000_000_000 // 1e15
)

// Manager 管理分支记录、到期堆、账本与全局单调时钟。
type Manager struct {
	mu       sync.Mutex
	lg       *ledger.Ledger
	ttl      int64
	capacity int
	clock    int64
	branches map[branchKey]*Branch
	entries  map[branchKey]*heapEntry
	heap     *expiryHeap
	examined int // 上一次操作考察的堆项数（≤到期数+1）
}

// New 创建管理器；ttl ∈ [1,1e9]，容量 N ∈ [1,1e6]，非法返回 ledger.ErrParam。
func New(lg *ledger.Ledger, ttl int64, capacity int) (*Manager, error) {
	if ttl < 1 || ttl > MaxTTL || capacity < 1 || capacity > MaxCap {
		return nil, ledger.ErrParam
	}
	return &Manager{
		lg:       lg,
		ttl:      ttl,
		capacity: capacity,
		branches: map[branchKey]*Branch{},
		entries:  map[branchKey]*heapEntry{},
		heap:     newExpiryHeap(),
	}, nil
}

// Ledger 暴露底层账本（coord 充值等场景使用）。
func (m *Manager) Ledger() *ledger.Ledger { return m.lg }

// Clock 返回当前全局时钟。
func (m *Manager) Clock() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.clock
}

// AdvanceClock 仅推进全局时钟（无到期效果），供 Begin 等只产生日志的协调操作使用。
// 非法或倒退返回对应错误且不推进。
func (m *Manager) AdvanceClock(now int64) error {
	if err := checkNow(now); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if now < m.clock {
		return ledger.ErrClock
	}
	m.clock = now
	return nil
}

// BranchCount 返回当前分支记录数（含终态记录，因取消标记永久保留）。
func (m *Manager) BranchCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.branches)
}

// Examined 返回上一次带 now 操作考察的堆项数。
func (m *Manager) Examined() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.examined
}

func checkNow(now int64) error {
	if now < 0 || now > MaxNow {
		return ledger.ErrParam
	}
	return nil
}

// popDueLocked 把 now（含恰等）之前到期的堆项临时弹出但不改记录与账目，
// 供操作做“虚拟到期”判定；考察数为到期数，或堆非空时的到期数+1。
func (m *Manager) popDueLocked(now int64) []*heapEntry {
	var due []*heapEntry
	for m.heap.len() > 0 && m.heap.items[0].deadline <= now {
		due = append(due, m.heap.pop())
	}
	m.examined = len(due)
	if m.heap.len() > 0 {
		m.examined++ // 多查看堆顶 1 项即确认无更多到期
	}
	return due
}

// applyDueLocked 落实到期：释放冻结并置 Cancelled(Expired)。仅在操作成功时调用。
func (m *Manager) applyDueLocked(due []*heapEntry) {
	for _, e := range due {
		delete(m.entries, e.key)
		b := m.branches[e.key]
		if b != nil && b.State == StateTried {
			m.lg.Unfreeze(b.Acct, b.Amount)
			b.State = StateCancelled
			b.Reason = CancelExpired
		}
	}
}

// restoreDueLocked 拒绝路径：把临时弹出的到期项原样放回，不落实任何到期效果。
func (m *Manager) restoreDueLocked(due []*heapEntry) {
	for _, e := range due {
		m.heap.push(e)
		m.entries[e.key] = e
	}
}

func dueContains(due []*heapEntry, key branchKey) bool {
	for _, e := range due {
		if e.key == key {
			return true
		}
	}
	return false
}

// releasedOnAcct 计算到期项中某账户将被虚拟释放的冻结额。
func (m *Manager) releasedOnAcct(due []*heapEntry, acct string) int64 {
	var released int64
	for _, e := range due {
		b := m.branches[e.key]
		if b != nil && b.State == StateTried && string(b.Acct) == acct {
			released += b.Amount
		}
	}
	return released
}

// Get 返回分支记录副本；不存在返回 nil,false。
func (m *Manager) Get(xid, br []byte) (Branch, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.branches[branchKey{string(xid), string(br)}]
	if !ok {
		return Branch{}, false
	}
	out := *b
	out.Acct = append([]byte(nil), b.Acct...)
	return out, true
}

// Avail 只读返回账户在 now 时刻的可用额；到期按 now 虚拟处理后读数，
// 不改任何账目与记录，也不推进时钟。
func (m *Manager) Avail(acct []byte, now int64) (int64, error) {
	if len(acct) == 0 {
		return 0, ledger.ErrParam
	}
	if err := checkNow(now); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if now < m.clock {
		return 0, ledger.ErrClock
	}
	due := m.popDueLocked(now)
	v := m.lg.Avail(acct) + m.releasedOnAcct(due, string(acct))
	m.restoreDueLocked(due)
	return v, nil
}
