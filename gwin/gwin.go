// Package gwin 按 Key 维护全局累积器与快照历史。
// 窗口全局无限：触发只“报告当前累计”，append 一条快照，绝不重置累积器。
package gwin

import (
	"errors"
	"sync"

	"ontology/gw"
)

// 三类互不相同的哨兵错误：周期非法 / 空 Key / 快照数超限。
var (
	ErrInvalidPeriod = errors.New("gwin: period must be positive")
	ErrEmptyKey      = errors.New("gwin: empty key")
	ErrSnapshotLimit = errors.New("gwin: snapshot count would exceed maxSnap")
)

// Event 是输入元素：Key 与其整数值 Val（任意整数，含负数）。
type Event struct {
	Key string
	Val int64
}

// Snapshot 是一次触发时的累计快照（累计值，不是增量）。
type Snapshot struct {
	Key string
	Sum int64
	Cnt int64
}

// entry 是单个 Key 的全部状态。
type entry struct {
	acc   gw.Acc
	snaps []Snapshot
	// readCnt 记录“最近一次触发时为计算快照而读取的已累积元素个数”。
	// 快照直接取运行中的 sum/cnt（O(1)），不扫描历史元素，所以恒为 0。
	// 仅本包测试可直接读；绝不经任何导出 API 暴露。
	readCnt int64
}

// sim 是整批提交前的影子演算状态，只需要累计器与将达快照数。
type sim struct {
	acc gw.Acc
	n   int
}

// Table 并发安全地持有所有 Key 的累积状态。
type Table struct {
	mu      sync.RWMutex
	period  int64
	maxSnap int
	keys    map[string]*entry
}

// New 创建 Table：period 必须为正整数。
func New(period int64, maxSnap int) (*Table, error) {
	if period <= 0 {
		return nil, ErrInvalidPeriod
	}
	return &Table{period: period, maxSnap: maxSnap, keys: map[string]*entry{}}, nil
}

// Feed 原子地接受一整批元素：任一条被拒（空 Key / 超限）则整批不生效。
// 先影子演算整批，全部通过后才在真实状态上重放，保证失败不留痕。
func (t *Table) Feed(evs []Event) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	shadow := map[string]*sim{}
	for _, ev := range evs { // 阶段一：影子演算，只读 + 临时副本
		if ev.Key == "" {
			return ErrEmptyKey
		}
		s := shadow[ev.Key]
		if s == nil {
			s = &sim{}
			if e := t.keys[ev.Key]; e != nil {
				s.acc, s.n = e.acc, len(e.snaps)
			}
			shadow[ev.Key] = s
		}
		s.acc.Add(ev.Val)
		if gw.Triggered(s.acc.Cnt, t.period) {
			if s.n+1 > t.maxSnap {
				return ErrSnapshotLimit
			}
			s.n++
		}
	}

	for _, ev := range evs { // 阶段二：提交，按序重放
		e := t.keys[ev.Key]
		if e == nil {
			e = &entry{}
			t.keys[ev.Key] = e
		}
		e.acc.Add(ev.Val)
		if gw.Triggered(e.acc.Cnt, t.period) {
			// 直接对运行中的 sum/cnt 成像：读取元素数为 0，不触碰 sum/cnt。
			e.readCnt = 0
			e.snaps = append(e.snaps, Snapshot{Key: ev.Key, Sum: e.acc.Sum, Cnt: e.acc.Cnt})
		}
	}
	return nil
}

// Snapshots 返回该 Key 快照列表的副本（按触发顺序、cnt 严格递增）。
func (t *Table) Snapshots(key string) []Snapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()
	e := t.keys[key]
	if e == nil || len(e.snaps) == 0 {
		return nil
	}
	out := make([]Snapshot, len(e.snaps))
	copy(out, e.snaps)
	return out
}

// Totals 返回该 Key 当前的累计和与累计个数。
func (t *Table) Totals(key string) (sum, cnt int64) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if e := t.keys[key]; e != nil {
		return e.acc.Sum, e.acc.Cnt
	}
	return 0, 0
}
