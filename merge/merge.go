// Package merge 在 pwm 之上维护多分区合并水位：时钟单调校验、按 last 有序的空闲定位、非空闲最小值维护与只进不退的合并。
package merge

import (
	"container/heap"
	"errors"

	"ontology/pwm"
)

var ErrBadArgument = errors.New("merge: n and idle must be positive")
var ErrPartitionRange = errors.New("merge: partition index out of range")
var ErrClockRewind = errors.New("merge: injected clock went backwards")
var ErrWatermarkRewind = errors.New("merge: partition watermark went backwards")

type Manager struct {
	parts         []*pwm.Part
	lastH, waterH intHeap // lastH 按 last 升序定位空闲；waterH 按 water 升序取候选
	idle          []bool
	idleTo, clock int64 // clock = 上一次成功操作的 now
	merged, reads int64 // reads 非导出：最近一次操作中被逐个读取的分区个数
}

// intHeap 只含非空闲分区；上浮下沉比较是结构内部操作，不计 reads。
type intHeap struct {
	h, pos []int
	water  bool
	m      *Manager
}

func (x *intHeap) Len() int { return len(x.h) }
func (x *intHeap) Less(i, j int) bool {
	a, b := x.h[i], x.h[j]
	wa, wb := x.m.parts[a].Water(), x.m.parts[b].Water()
	if x.water {
		return wa < wb || (wa == wb && a < b)
	}
	la, lb := x.m.parts[a].Last(), x.m.parts[b].Last()
	return la < lb || (la == lb && a < b)
}
func (x *intHeap) Swap(i, j int) {
	x.h[i], x.h[j] = x.h[j], x.h[i]
	x.pos[x.h[i]], x.pos[x.h[j]] = i, j
}
func (x *intHeap) Push(v any)   { x.h = append(x.h, v.(int)) }
func (x *intHeap) Pop() (v any) { v, x.h = x.h[len(x.h)-1], x.h[:len(x.h)-1]; return }

func New(n int, idle, t0 int64) (*Manager, error) {
	if n <= 0 || idle <= 0 {
		return nil, ErrBadArgument
	}
	m := &Manager{idleTo: idle, clock: t0, merged: pwm.NegInf, idle: make([]bool, n)}
	m.lastH = intHeap{pos: make([]int, n), m: m}
	m.waterH = intHeap{pos: make([]int, n), water: true, m: m}
	for p := range n {
		m.parts = append(m.parts, pwm.New(t0))
		m.lastH.h, m.waterH.h = append(m.lastH.h, p), append(m.waterH.h, p)
		m.lastH.pos[p], m.waterH.pos[p] = p, p
	}
	return m, nil
}
func (m *Manager) Merged() int64   { return m.merged }
func (m *Manager) Idle(p int) bool { return m.idle[p] }

func (m *Manager) peel(now int64) {
	for m.lastH.Len() > 0 {
		p := m.lastH.h[0]
		m.reads++ // 逐个读取一个分区判空闲
		if !m.parts[p].Idle(now, m.idleTo) {
			return
		}
		heap.Pop(&m.lastH)
		m.lastH.pos[p] = -1
		heap.Remove(&m.waterH, m.waterH.pos[p])
		m.waterH.pos[p], m.idle[p] = -1, true
	}
}

func (m *Manager) advance() {
	if m.waterH.Len() == 0 {
		return
	}
	m.reads++ // 逐个读取堆顶分区求最小值
	if c := m.parts[m.waterH.h[0]].Water(); c > m.merged {
		m.merged = c
	}
}

func (m *Manager) Report(p int, w, now int64) (int64, error) {
	if p < 0 || p >= len(m.parts) {
		return m.merged, ErrPartitionRange
	}
	if now < m.clock {
		return m.merged, ErrClockRewind
	}
	if !m.parts[p].CanReport(w) {
		return m.merged, ErrWatermarkRewind
	}
	m.reads = 0
	m.clock = now
	m.peel(now)
	woke := m.idle[p] // peel 后若本分区已空闲，本次上报必令其立即恢复
	m.parts[p].Report(w, now)
	if woke {
		m.idle[p] = false
		m.waterH.pos[p] = m.waterH.Len()
		heap.Push(&m.waterH, p)
		m.lastH.pos[p] = m.lastH.Len()
		heap.Push(&m.lastH, p)
	} else {
		heap.Fix(&m.waterH, m.waterH.pos[p])
		heap.Fix(&m.lastH, m.lastH.pos[p])
	}
	m.advance()
	return m.merged, nil
}
func (m *Manager) Tick(now int64) (int64, error) {
	if now < m.clock {
		return m.merged, ErrClockRewind
	}
	m.reads = 0
	m.clock = now
	m.peel(now)
	m.advance()
	return m.merged, nil
}

func CheckReadBound() error {
	for _, n := range []int{100, 1000, 10000} {
		m, _ := New(n, 1<<60, 0)
		for p := range n {
			m.Report(p, int64(n-p), 0)
		} // p0 最高，最小值在 p=n-1
		if _, e := m.Report(0, int64(n)+1, 0); e != nil || m.reads > 3 { // 空闲态、最小值归属均不变
			return errors.New("merge: steady-state read count grows with m")
		}
	}
	const n, k = 1000, 20
	m, _ := New(n, 10, 0)
	for p := range n {
		m.Report(p, int64(p+1), 0)
	}
	for p := k; p < n; p++ {
		m.Report(p, int64(p+101), 9)
	} // 后 n-k 个 last=9
	if _, e := m.Tick(10); e != nil || m.reads > 2*k+5 { // Tick(10) 恰有 k 个转空闲
		return errors.New("merge: idle-transition read count too wide")
	}
	return nil
}
