package fib

import (
	"fmt"
	"sync"
)

// Manager 是容量受限的转发表管理器。所有方法可并发调用,
// 效果等价于某个串行顺序;读操作看到的控制面与数据面来自同一次
// 更新之后(由同一把读写锁保护同一份状态)。
type Manager struct {
	mu    sync.RWMutex
	tr    trie
	cap   int
	stats Stats
}

// NewManager 创建容量上限为 capacity 的管理器。capacity 必须非负,
// 否则 panic(配置错误)。
func NewManager(capacity int) *Manager {
	if capacity < 0 {
		panic("fib: 容量上限不能为负")
	}
	m := &Manager{cap: capacity}
	m.tr.stats = &m.stats
	return m
}

// undoEntry 记录一条路由在更新前的状态,用于容量不足或批量失败时
// 整体回滚。
type undoEntry struct {
	prefix Prefix
	old    color
	had    bool
}

// rollback 按逆序撤销已生效的修改,保证控制面、数据面与条目数
// 恢复到更新前的状态。
func (m *Manager) rollback(undos []undoEntry) {
	for i := len(undos) - 1; i >= 0; i-- {
		u := undos[i]
		if u.had {
			m.tr.applyPut(u.prefix, u.old)
		} else {
			m.tr.applyDelete(u.prefix)
		}
	}
}

// checkCapacity 在更新生效后校验最少条目数,超限时回滚并返回
// ErrCapacityExceeded。undos 为 nil 表示本次更新未改变状态。
func (m *Manager) checkCapacity(undos []undoEntry) error {
	if m.tr.count() > m.cap {
		m.rollback(undos)
		return ErrCapacityExceeded
	}
	return nil
}

// Put 写入一条路由;同前缀已存在则覆盖下一跳。写入与原值完全相同
// 视为空操作:成功且不改任何状态。生效后最少条目数超过容量上限时
// 整体拒绝,状态不变。
func (m *Manager) Put(p Prefix, nh Nexthop) error {
	if !p.Valid() || !nh.valid() {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	old, had, changed := m.tr.applyPut(p, nexthopColor(nh))
	if !changed {
		return nil
	}
	return m.checkCapacity([]undoEntry{{p, old, had}})
}

// Delete 撤销一条路由;前缀不存在时报 ErrRouteNotFound。
// 生效后最少条目数超过容量上限时整体拒绝,状态不变。
func (m *Manager) Delete(p Prefix) error {
	if !p.Valid() {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	old, found := m.tr.applyDelete(p)
	if !found {
		return ErrRouteNotFound
	}
	return m.checkCapacity([]undoEntry{{p, old, true}})
}

// Batch 批量更新:若干写入与撤销按给定顺序依次生效,同一批内可对
// 同一前缀重复操作。整批全有或全无:任一步参数非法(优先级最高,
// 先于一切执行前统一校验)、某步撤销目标不存在(按生效顺序取第一个)
// 或最终最少条目数超容量,整批不生效。
func (m *Manager) Batch(ops ...Op) error {
	// 第一优先级:参数非法。先统一校验,避免与执行期错误混淆。
	for i := range ops {
		op := &ops[i]
		if !op.Prefix.Valid() || (!op.Delete && !op.Nexthop.valid()) {
			return fmt.Errorf("%w: 批量第 %d 步", ErrInvalidArgument, i)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	undos := make([]undoEntry, 0, len(ops))
	for i, op := range ops {
		if op.Delete {
			old, found := m.tr.applyDelete(op.Prefix)
			if !found {
				m.rollback(undos)
				return fmt.Errorf("%w: 批量第 %d 步", ErrRouteNotFound, i)
			}
			undos = append(undos, undoEntry{op.Prefix, old, true})
			continue
		}
		old, had, changed := m.tr.applyPut(op.Prefix, nexthopColor(op.Nexthop))
		if changed {
			undos = append(undos, undoEntry{op.Prefix, old, had})
		}
	}
	return m.checkCapacity(undos)
}

// SetCapacity 在线调整容量上限。新上限小于当前最少条目数时报
// ErrCapacityExceeded 且不改变任何状态;调整本身不触发任何路由变化。
func (m *Manager) SetCapacity(c int) error {
	if c < 0 {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if c < m.tr.count() {
		return ErrCapacityExceeded
	}
	m.cap = c
	return nil
}

// Capacity 返回当前容量上限。
func (m *Manager) Capacity() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cap
}

// Query 查询 addr 在控制面与数据面上的结果,两个结果来自同一次
// 更新之后。
func (m *Manager) Query(addr uint32) (control, data Result) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.tr.controlQuery(addr).result(), m.tr.dataQuery(addr).result()
}

// Count 返回当前数据面的最少条目数。
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.tr.count()
}

// List 列出全部数据面条目,按起始地址升序、同起始地址按前缀长度
// 升序排列。返回的条目数与同一时刻的 Count 一致。
func (m *Manager) List() []Entry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.tr.list()
}

// Stats 返回更新路径上的工作量计数(用于验证更新开销与无关路由
// 总数无关),并重置计数。
func (m *Manager) Stats() Stats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stats
}

// ResetStats 清零工作量计数。
func (m *Manager) ResetStats() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stats = Stats{}
}
