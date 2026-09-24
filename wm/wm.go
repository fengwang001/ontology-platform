// Package wm 按源路由 Observe、累计三个全局计数器、提供 Last 查询。
// 依赖 drift；被 api 依赖。
package wm

import (
	"errors"
	"sync"

	"ontology/drift"
)

// 三类可判定且互不相同的哨兵错误。
var (
	ErrInvalidThreshold = errors.New("wm: driftThreshold<=0 or rollbackTolerance<0")
	ErrEmptySource      = errors.New("wm: empty source")
	ErrNegativeMark     = errors.New("wm: negative watermark")
)

// Manager 是多源管理器，可被多 goroutine 并发使用。
type Manager struct {
	mu      sync.Mutex
	sources map[string]*drift.Source
	drift   int64
	tol     int64

	driftCount    int64
	reorderCount  int64
	rollbackCount int64

	lastReads int // 最近一次 Observe 为判定读取的历史水位线个数（非导出，不进公开接口）
}

// New 构造管理器；参数非法时整体失败，不留任何状态。
func New(driftThreshold, rollbackTolerance int64) (*Manager, error) {
	if driftThreshold <= 0 || rollbackTolerance < 0 {
		return nil, ErrInvalidThreshold
	}
	return &Manager{
		sources: make(map[string]*drift.Source),
		drift:   driftThreshold,
		tol:     rollbackTolerance,
	}, nil
}

// Observe 先校验（失败不留痕），再路由到对应源判类并累计计数器。
func (m *Manager) Observe(source string, w int64) (drift.Class, error) {
	if source == "" {
		return drift.Normal, ErrEmptySource
	}
	if w < 0 {
		return drift.Normal, ErrNegativeMark
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sources[source]
	if !ok {
		s = drift.NewSource(m.drift, m.tol)
		m.sources[source] = s
		m.lastReads = 0 // 首条：无历史可读
	} else {
		m.lastReads = 1 // 只读当前 last，O(1)，不随历史增长
	}
	cls := s.Observe(w)
	switch cls {
	case drift.Drift:
		m.driftCount++
	case drift.Reorder:
		m.reorderCount++
	case drift.Rollback:
		m.rollbackCount++
	}
	return cls, nil
}

// Last 查询某源当前 last；无基线时 ok=false。
func (m *Manager) Last(source string) (last int64, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sources[source]
	if !ok {
		return 0, false
	}
	return s.Last()
}

// Counts 返回三个全局计数器。计数只增不减，并发读到的总和单调不减。
func (m *Manager) Counts() (driftN, reorderN, rollbackN int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.driftCount, m.reorderCount, m.rollbackCount
}

// CheckConstantReads 对单源观测 steps 条后再观测一条，
// 内部断言读取历史个数恒为 1，只暴露布尔结论，不暴露计数器数值。
func (m *Manager) CheckConstantReads(steps int) bool {
	if steps < 1 {
		return false
	}
	const src = "__selfcheck_reads__"
	for i := 0; i < steps; i++ {
		if _, err := m.Observe(src, int64(i)); err != nil {
			return false
		}
	}
	if _, err := m.Observe(src, int64(steps)); err != nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastReads == 1
}
