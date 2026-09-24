// Package reb 实现物化视图的分块重建状态机，依赖 agg 做计数聚合。
package reb

import (
	"errors"
	"sync"

	"ontology/agg"
)

// 四类可判定、互不相同的哨兵错误。
var (
	ErrBadChunk    = errors.New("reb: chunk must be >= 1")
	ErrBusy        = errors.New("reb: rebuild already in progress")
	ErrNotBuilding = errors.New("reb: no rebuild in progress")
	ErrIncomplete  = errors.New("reb: commit refused: rebuild incomplete")
)

type checkpoint struct {
	processed int
	shadow    map[string]int
}

// State 是诊断快照，供演示/自检观察状态；刻意不含内部计数器。
type State struct {
	Processed, Gen, CPProcessed int
	Building                    bool
	Shadow, CPShadow, Committed map[string]int // Shadow 未在重建时为 nil
}

// Machine 是重建状态机。applied 为非导出计数器：累计应用到 shadow 的
// src 项数（含续跑重放），只能由包内测试读取，不暴露到任何公开接口。
type Machine struct {
	mu                sync.RWMutex
	src               []string
	chunk             int
	committed, shadow map[string]int
	cp                checkpoint
	processed, gen    int
	building          bool
	applied           int64
}

// New 构造状态机；chunk<1 整体拒绝，不产生任何半成品。
func New(src []string, chunk int) (*Machine, error) {
	if chunk < 1 {
		return nil, ErrBadChunk
	}
	s := make([]string, len(src))
	copy(s, src)
	return &Machine{src: s, chunk: chunk, committed: map[string]int{},
		cp: checkpoint{shadow: map[string]int{}}}, nil
}

func clone(m map[string]int) map[string]int {
	c := make(map[string]int, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

// Snapshot 返回当前状态的独立副本（不含内部计数器）。
func (m *Machine) Snapshot() State {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return State{Processed: m.processed, Gen: m.gen, Building: m.building,
		Shadow: clone(m.shadow), CPProcessed: m.cp.processed,
		CPShadow: clone(m.cp.shadow), Committed: clone(m.committed)}
}

// Start 开始/继续重建；已在建则 ErrBusy。从检查点恢复 shadow 与 processed。
func (m *Machine) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.building {
		return ErrBusy
	}
	m.building = true
	m.shadow = clone(m.cp.shadow)
	m.processed = m.cp.processed
	return nil
}

// Step 把下一块逐项计入 shadow，前移 processed，并写检查点（独立副本）。
func (m *Machine) Step() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.building {
		return ErrNotBuilding
	}
	if m.processed == len(m.src) {
		return nil
	}
	end := m.processed + m.chunk
	if end > len(m.src) {
		end = len(m.src)
	}
	seg := m.src[m.processed:end]
	agg.Add(m.shadow, seg)
	m.applied += int64(len(seg))
	m.processed = end
	m.cp.processed = end
	m.cp.shadow = clone(m.shadow)
	return nil
}

// Commit 在全部处理完后原子切换到新视图并推进 gen；否则拒绝且不留痕。
func (m *Machine) Commit() (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.building {
		return 0, ErrNotBuilding
	}
	if m.processed < len(m.src) {
		return 0, ErrIncomplete
	}
	m.committed = clone(m.shadow)
	m.gen++
	g := m.gen
	m.building = false
	m.shadow = nil
	return g, nil
}

// Crash 模拟崩溃：丢弃 shadow，building 复位；检查点保持不变，可续跑。
func (m *Machine) Crash() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.building {
		return ErrNotBuilding
	}
	m.building = false
	m.shadow = nil
	return nil
}

// View 返回已提交视图的副本；重建全程只反映旧视图，shadow 不可见。
func (m *Machine) View() map[string]int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return clone(m.committed)
}

// Gen 返回当前已提交代数。
func (m *Machine) Gen() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.gen
}
