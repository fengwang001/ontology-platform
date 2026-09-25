// Package sw 实现快照/增量切换与累加状态机。依赖 snap。
package sw

import (
	"sync"

	"ontology/snap"
)

// Switcher 持有物化视图状态：state 累加表与已应用位点 applied。
// 所有校验先于任何写操作，被拒绝的操作不改变任何状态。
type Switcher struct {
	mu       sync.RWMutex
	state    map[string]int64
	applied  int64
	lastScan int // 非导出：最近一次 ApplyIncremental 扫描过的 state 条目数
}

// New 返回一个空实例（applied = 0，等价于已应用空快照 SP=0）。
func New() *Switcher {
	return &Switcher{state: make(map[string]int64)}
}

// ApplySnapshot 冷启动第一步：state = Table 的拷贝，applied = snap.SP。
// 快照非法（SP < 0）时整体失败，状态不变。
func (s *Switcher) ApplySnapshot(sn snap.Snapshot) error {
	if err := snap.CheckSnapshot(sn); err != nil {
		return err
	}
	cp := make(map[string]int64, len(sn.Table))
	for k, v := range sn.Table {
		cp[k] = v
	}
	s.mu.Lock()
	s.state = cp
	s.applied = sn.SP
	s.mu.Unlock()
	return nil
}

// ApplyIncremental 应用一条增量事件：要求 ev.Pos == applied+1。
// 任何校验失败都不改变 state 与 applied。
func (s *Switcher) ApplyIncremental(ev snap.Event) error {
	if err := snap.CheckEvent(ev); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := snap.CheckContiguous(s.applied, ev); err != nil {
		return err
	}
	// O(1) 的 map 单点更新：只触碰 ev.Key 一个条目，不重扫整张表。
	s.state[ev.Key] += ev.Delta
	s.lastScan = 1
	s.applied = ev.Pos
	return nil
}

// View 返回 state 的独立拷贝，调用方可安全持有与遍历。
func (s *Switcher) View() map[string]int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]int64, len(s.state))
	for k, v := range s.state {
		out[k] = v
	}
	return out
}

// Applied 返回当前已应用的最大位点。
func (s *Switcher) Applied() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.applied
}
