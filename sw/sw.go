// Package sw 实现冷启动切换与累加：ApplySnapshot / ApplyIncremental / View。
// 依赖 snap，不反向依赖。
package sw

import (
	"sync"

	"ontology/snap"
)

// Switcher 持有一份物化视图状态，applied 为已覆盖的最大位点。
type Switcher struct {
	mu      sync.RWMutex
	state   map[string]int64
	applied int64
	// scanned 记录最近一次 ApplyIncremental 扫描过的 state 条目个数。
	// 非导出，不出现在任何公开接口；增量是 O(1) map 更新，恒为 1。
	scanned int
}

// New 返回空 Switcher，applied 为 0（位点从 1 开始）。
func New() *Switcher {
	return &Switcher{state: make(map[string]int64)}
}

// ApplySnapshot state = Table 的拷贝，applied = SP。失败不留痕。
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

// ApplyIncremental 要求 ev.Pos == applied+1，否则整体失败且状态不变。
// 校验全部先于写操作，拒绝即返回，state 与 applied 均不动。
func (s *Switcher) ApplyIncremental(ev snap.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := snap.CheckEvent(s.applied, ev); err != nil {
		return err
	}
	s.scanned = 1 // 单次 map 更新只触碰一个条目，不重扫快照
	s.state[ev.Key] += ev.Delta
	s.applied = ev.Pos
	return nil
}

// View 返回 state 的独立拷贝，调用方修改不影响内部状态。
func (s *Switcher) View() map[string]int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := make(map[string]int64, len(s.state))
	for k, v := range s.state {
		cp[k] = v
	}
	return cp
}

// Applied 返回当前已覆盖的最大位点。
func (s *Switcher) Applied() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.applied
}
