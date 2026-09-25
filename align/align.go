// Package align 按对齐水位线 W=min(wA,wB) 缓冲并按 TS 升序发出事件。
package align

import (
	"container/heap"

	"ontology/wm"
)

// Event 是一条带事件时间的流事件，Stream 取 'A' 或 'B'。
type Event struct {
	Stream byte
	TS     int64
}

// item 是缓冲条目，seq 为到达序号（同 TS 时按到达序发出）。
type item struct {
	ev  Event
	seq int64
}

// pq 是按 (TS, seq) 排序的最小堆；cmps 指向 Less 调用计数器。
type pq struct {
	items []item
	cmps  *int
}

func (p pq) Len() int { return len(p.items) }

func (p pq) Less(i, j int) bool {
	*p.cmps++
	a, b := p.items[i], p.items[j]
	if a.ev.TS != b.ev.TS {
		return a.ev.TS < b.ev.TS
	}
	return a.seq < b.seq
}

func (p pq) Swap(i, j int) { p.items[i], p.items[j] = p.items[j], p.items[i] }

func (p *pq) Push(x any) { p.items = append(p.items, x.(item)) }

func (p *pq) Pop() any {
	old := p.items
	p.items = old[:len(old)-1]
	return old[len(old)-1]
}

// Aligner 缓冲被接受的事件，W 越过谁就把谁按 TS 升序发出。非并发安全。
type Aligner struct {
	wa, wb   wm.Watermark
	queue    pq
	seq      int64
	lessCmps int
	lastCmps int // 最近一次 drain 中为定位最小 TS 的比较次数（非导出）
}

// New 返回一个空 Aligner。
func New() *Aligner { return new(Aligner).init() }

func (a *Aligner) init() *Aligner {
	a.queue.cmps = &a.lessCmps
	return a
}

// W 返回对齐水位线；ok 为 false 表示仍有流未见事件（W 为负无穷）。
func (a *Aligner) W() (w int64, ok bool) {
	va, oka := a.wa.Value()
	vb, okb := a.wb.Value()
	if !oka || !okb {
		return 0, false
	}
	return min(va, vb), true
}

// Feed 记录 ev：迟到则丢弃（accepted=false，状态不变），否则入缓冲并
// 发出所有 TS <= W 的事件（TS 升序，同 TS 按到达序）。调用方保证 ev 合法。
func (a *Aligner) Feed(ev Event) (emitted []Event, accepted bool) {
	if a.queue.cmps == nil {
		a.init()
	}
	w := &a.wa
	if ev.Stream == 'B' {
		w = &a.wb
	}
	if !w.Advance(ev.TS) {
		return nil, false
	}
	heap.Push(&a.queue, item{ev: ev, seq: a.seq})
	a.seq++
	return a.drain(), true
}

// Close 把两条流水位线推到正无穷并清空缓冲。
func (a *Aligner) Close() []Event {
	if a.queue.cmps == nil {
		a.init()
	}
	a.wa.Close()
	a.wb.Close()
	return a.drain()
}

// drain 发出所有 TS <= W 的缓冲事件；用最小堆定位最小 TS，不做线性扫描。
func (a *Aligner) drain() []Event {
	a.lessCmps = 0
	defer func() { a.lastCmps = a.lessCmps }()
	w, ok := a.W()
	if !ok {
		return nil
	}
	var out []Event
	for len(a.queue.items) > 0 && a.queue.items[0].ev.TS <= w {
		out = append(out, heap.Pop(&a.queue).(item).ev)
	}
	return out
}
