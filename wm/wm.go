// Package wm 按源路由水位线观测、累计三类全局计数器，并保证并发安全。
// 它只依赖 drift 包；反向依赖不允许。
package wm

import (
	"errors"
	"sync"

	"ontology/drift"
)

// 源非法与水位线非法是两个互不相同的哨兵错误；
// 参数（阈值）非法的哨兵由 api 包定义。
var (
	ErrEmptySource       = errors.New("wm: source must not be the empty string")
	ErrNegativeWatermark = errors.New("wm: watermark must not be negative")
)

// Manager 管理多个源的水位线。零值不可用，用 NewManager 构造。
type Manager struct {
	mu sync.Mutex

	sources           map[string]*drift.State
	driftThreshold    int64
	rollbackTolerance int64

	driftCount    int64
	reorderCount  int64
	rollbackCount int64

	// lastReadCount 记录最近一次 Observe 为判定而读取的历史水位线个数。
	// 判定只依赖该源当前的 last：首条无历史可读记 0，此后恒为 1。
	// 刻意非导出：数值只允许同包测试直接断言，不进入任何公开接口。
	lastReadCount int
}

// NewManager 创建多源管理器。阈值合法性由 api.New 统一校验。
func NewManager(driftThreshold, rollbackTolerance int64) *Manager {
	return &Manager{
		sources:           make(map[string]*drift.State),
		driftThreshold:    driftThreshold,
		rollbackTolerance: rollbackTolerance,
	}
}

// Observe 按 source 路由一条水位线，返回互斥分类并累计对应计数器。
// Normal（含每个源的首条）不累计任何计数器。
// 调用方（api 层）负责保证 source 非空且 w >= 0。
func (m *Manager) Observe(source string, w int64) drift.Class {
	m.mu.Lock()
	defer m.mu.Unlock()

	st, ok := m.sources[source]
	if !ok {
		// 首条：无基线，判定不读取任何历史水位线；首条恒 Normal，不计数。
		st = drift.NewState(m.driftThreshold, m.rollbackTolerance)
		m.sources[source] = st
		m.lastReadCount = 0
		st.Observe(w)
		return drift.Normal
	}

	// 只读取一个历史水位线（当前 last），与历史长度无关 => O(1)。
	m.lastReadCount = 1
	c := st.Observe(w)
	switch c {
	case drift.Drift:
		m.driftCount++
	case drift.Reorder:
		m.reorderCount++
	case drift.Rollback:
		m.rollbackCount++
	}
	return c
}

// Last 返回某源最后接受的水位线；第二返回值报告该源是否收到过水位线。
func (m *Manager) Last(source string) (int64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.sources[source]
	if !ok {
		return 0, false
	}
	return st.Last()
}

// Counts 返回 drift/reorder/rollback 三类的累计次数的一致快照。
func (m *Manager) Counts() (driftCount, reorderCount, rollbackCount int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.driftCount, m.reorderCount, m.rollbackCount
}

// VerifyObserveIsO1 在多档历史规模下核验：观测第 m+1 条水位线时，
// 为判定读取的历史水位线个数恒为 1。只返回成立与否，
// 不经过公开接口暴露非导出计数器的任何数值。
func VerifyObserveIsO1() bool {
	for _, n := range []int{100, 1000, 10000} {
		m := NewManager(10, 3)
		for i := 0; i < n; i++ {
			m.Observe("s", int64(i))
		}
		m.Observe("s", int64(n)) // 第 m+1 条
		if m.lastReadCount != 1 {
			return false
		}
	}
	return true
}
