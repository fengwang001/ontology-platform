// Package gwin 按 Key 维护全局窗口：累积器、快照历史与触发输出。依赖 gw。
// Manager 本身不加锁，并发安全由上层（api）保证。
package gwin

import (
	"errors"

	"ontology/gw"
)

// 三类可判定哨兵错误，互不相同。
var (
	ErrBadPeriod    = errors.New("gwin: period must be positive")
	ErrEmptyKey     = errors.New("gwin: empty key")
	ErrTooManySnaps = errors.New("gwin: snapshot limit exceeded")
)

// Event 是一个输入元素。
type Event struct {
	Key string
	Val int64
}

// Snapshot 是一次触发输出的累计快照（含触发它的元素，不是增量）。
type Snapshot struct {
	Sum int64
	Cnt int64
}

// window 是单个 Key 的全局窗口。
type window struct {
	acc   gw.Acc
	snaps []Snapshot
	reads int64 // 最近一次触发时为算快照重读的已累积元素个数；O(1) 实现恒为 0
}

// Manager 按 Key 管理一组窗口。
type Manager struct {
	period  int64
	maxSnap int
	keys    map[string]*window
}

// NewManager 校验 period，非法即 ErrBadPeriod。
func NewManager(period int64, maxSnap int) (*Manager, error) {
	if period <= 0 {
		return nil, ErrBadPeriod
	}
	return &Manager{period: period, maxSnap: maxSnap, keys: map[string]*window{}}, nil
}

// Apply 原子地喂入一批元素：任一被拒则整批不生效（不变量 4）。
// 两段式：先校验 + 模拟，全部通过后才落状态。
func (m *Manager) Apply(evs []Event) error {
	for _, e := range evs {
		if e.Key == "" {
			return ErrEmptyKey
		}
	}
	// 模拟每个 Key 本批会新增几次触发，核对 maxSnap。
	type sim struct {
		cnt   int64
		added int
	}
	sims := map[string]*sim{}
	for _, e := range evs {
		s := sims[e.Key]
		if s == nil {
			s = &sim{cnt: m.keys[e.Key].accCnt()}
			sims[e.Key] = s
		}
		s.cnt++
		if s.cnt%m.period == 0 {
			s.added++
		}
	}
	for key, s := range sims {
		if m.keys[key].snapCount()+s.added > m.maxSnap {
			return ErrTooManySnaps
		}
	}
	// 全部通过，真正落状态：先累加，再判定，触发不重置。
	for _, e := range evs {
		w := m.keys[e.Key]
		if w == nil {
			w = &window{}
			m.keys[e.Key] = w
		}
		w.acc.Add(e.Val)
		if w.acc.Fired(m.period) {
			w.reads = 0 // 快照直接取运行中的 sum/cnt，重读 0 个已累积元素
			w.snaps = append(w.snaps, Snapshot{Sum: w.acc.Sum(), Cnt: w.acc.Cnt()})
		}
	}
	return nil
}

func (w *window) accCnt() int64 {
	if w == nil {
		return 0
	}
	return w.acc.Cnt()
}

func (w *window) snapCount() int {
	if w == nil {
		return 0
	}
	return len(w.snaps)
}

// Snapshots 返回该 Key 的快照历史副本（按触发顺序，cnt 严格递增）。
func (m *Manager) Snapshots(key string) []Snapshot {
	w := m.keys[key]
	if w == nil {
		return nil
	}
	out := make([]Snapshot, len(w.snaps))
	copy(out, w.snaps)
	return out
}

// Totals 返回该 Key 当前的累计 sum/cnt。
func (m *Manager) Totals(key string) (sum, cnt int64) {
	w := m.keys[key]
	if w == nil {
		return 0, 0
	}
	return w.acc.Sum(), w.acc.Cnt()
}

// SnapshotReadO1 报告该 Key 最近一次触发是否未重读已累积元素。
// 只给结论，不暴露计数器数值。
func (m *Manager) SnapshotReadO1(key string) bool {
	w := m.keys[key]
	return w != nil && w.reads == 0
}
