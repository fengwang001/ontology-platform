// Package chunk 实现 chunk 生命周期（低/高水位快照+修正）、Poll 过滤应用、视图与输出。依赖 wal。
package chunk

import (
	"errors"
	"maps"
	"slices"
	"sync"

	"ontology/wal"
)

var ErrBadRange = errors.New("chunk: 键范围非法 lo>=hi")
var ErrOverlap = errors.New("chunk: 键范围与已有 chunk 相交")
var ErrPhase = errors.New("chunk: 阶段顺序错误")
var ErrViewLimit = errors.New("chunk: 视图行数超出 maxRows")

// Out 是一条下游输出：+(Key,Val)（Op=Upsert）或 -(Key)（Op=Delete）。
type Out struct {
	Op  wal.Op
	Key int64
	Val string
}
type done struct{ lo, hi, h int64 } // 已完成 chunk：左闭右开 [lo,hi)，高水位 h
// Manager 维护 chunk 状态、下游视图与 Poll 处理位置。可并发调用。
type Manager struct {
	log     *wal.Log
	maxRows int
	mu      sync.RWMutex
	view    map[int64]string
	done    []done
	pos     int64 // Poll 已处理到的 LSN
	active  bool  // 有进行中的 chunk
	read    bool  // 已 ReadChunk
	lo, hi  int64 // 进行中 chunk 的范围
	low     int64 // 低水位 L
	snap    map[int64]string
	checked int // 最近一次 EndChunk 修正阶段检查过的日志条目个数（非导出）
}

func New(l *wal.Log, maxRows int) *Manager {
	return &Manager{log: l, maxRows: maxRows, view: map[int64]string{}}
}

// BeginChunk 记录低水位 L=当前日志位置，开始一个 chunk。
func (m *Manager) BeginChunk(lo, hi int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active {
		return ErrPhase
	}
	if lo >= hi {
		return ErrBadRange
	}
	for _, d := range m.done {
		if lo < d.hi && d.lo < hi { // 左闭右开区间相交判定
			return ErrOverlap
		}
	}
	m.active, m.read, m.snap = true, false, nil
	m.lo, m.hi, m.low = lo, hi, m.log.Pos()
	return nil
}

// ReadChunk 读取此刻源表中键落在 [lo,hi) 的全部行作为快照。
func (m *Manager) ReadChunk() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.active || m.read {
		return ErrPhase
	}
	m.snap, m.read = m.log.Range(m.lo, m.hi), true
	return nil
}

// EndChunk 记录高水位 H，把 (L,H] 内落在范围的条目按 LSN 序修正快照并写入视图。失败不留痕。
func (m *Manager) EndChunk() ([]Out, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.active || !m.read {
		return nil, ErrPhase
	}
	h := m.log.Pos()
	es := m.log.Between(m.low, h) // 直接定位 (L,H]，不从头扫描
	m.checked = len(es)
	rows := maps.Clone(m.snap)
	for _, e := range es {
		if e.Key < m.lo || e.Key >= m.hi {
			continue
		}
		if e.Op == wal.Upsert {
			rows[e.Key] = e.Val
		} else {
			delete(rows, e.Key)
		}
	}
	nv := maps.Clone(m.view)
	for k, v := range rows {
		nv[k] = v
	}
	if len(nv) > m.maxRows {
		return nil, ErrViewLimit // chunk 不完成，保持已读快照阶段
	}
	outs := make([]Out, 0, len(rows))
	for _, k := range slices.Sorted(maps.Keys(rows)) {
		outs = append(outs, Out{Op: wal.Upsert, Key: k, Val: rows[k]})
	}
	m.view, m.active, m.read, m.snap = nv, false, false, nil
	m.done = append(m.done, done{m.lo, m.hi, h})
	return outs, nil
}

// Poll 从上次位置后按 LSN 序处理：只应用键落在已完成 chunk 内且 LSN 大于其高水位的条目。失败不留痕。
func (m *Manager) Poll() ([]Out, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur := m.log.Pos()
	nv := maps.Clone(m.view)
	var outs []Out
	for i, e := range m.log.Between(m.pos, cur) {
		lsn := m.pos + 1 + int64(i)
		h := int64(1<<63 - 1) // 不在任何已完成 chunk 内时必被跳过
		for _, d := range m.done {
			if e.Key >= d.lo && e.Key < d.hi {
				h = d.h
			}
		}
		if lsn <= h {
			continue
		}
		if e.Op == wal.Upsert {
			nv[e.Key] = e.Val
			outs = append(outs, Out{Op: wal.Upsert, Key: e.Key, Val: e.Val})
		} else if _, has := nv[e.Key]; has {
			delete(nv, e.Key)
			outs = append(outs, Out{Op: wal.Delete, Key: e.Key})
		}
	}
	if len(nv) > m.maxRows {
		return nil, ErrViewLimit // 处理位置不推进
	}
	m.view, m.pos = nv, cur
	return outs, nil
}

func (m *Manager) View() map[int64]string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return maps.Clone(m.view)
}
