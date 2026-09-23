// Package wheel 实现可配 tick、槽数、层数的分层时间轮(非空集合跳跃 + 溢出堆)。
package wheel

import (
	"container/heap"
	"sync"

	"ontology/slot"
)

type Item struct {
	Deadline                int64
	Seq                     uint64
	Key                     int64
	elem                    *slot.Element
	level, slotIdx, heapIdx int
	inHeap                  bool
}

type Config struct {
	Tick          int64
	Slots, Levels int
	Start         int64
}

type Wheel struct {
	mu              sync.Mutex
	tick            int64
	slotsN, levels  int
	rings           []*slot.Ring
	nonEmpty        []map[int]struct{}
	over            overHeap
	curTick         int64
	visited, active int
}

func itemLess(x, y any) bool {
	a, b := x.(*Item), y.(*Item)
	return a.Deadline < b.Deadline || a.Deadline == b.Deadline && a.Seq < b.Seq
}

func ceilDiv(a, b int64) int64 {
	if a <= 0 {
		return 0
	}
	return (a + b - 1) / b
}

func New(c Config) *Wheel {
	if c.Tick <= 0 || c.Levels <= 0 || c.Slots&(c.Slots-1) != 0 {
		panic("wheel: invalid config")
	}
	w := &Wheel{tick: c.Tick, slotsN: c.Slots, levels: c.Levels, curTick: ceilDiv(c.Start, c.Tick)}
	for range c.Levels {
		w.rings = append(w.rings, slot.New(c.Slots))
		w.nonEmpty = append(w.nonEmpty, map[int]struct{}{})
	}
	return w
}

func (w *Wheel) span(level int) int64 {
	s := int64(1)
	for range level {
		s *= int64(w.slotsN)
	}
	return s
}

func (w *Wheel) Visited() int    { w.mu.Lock(); defer w.mu.Unlock(); return w.visited }
func (w *Wheel) Active() int     { w.mu.Lock(); defer w.mu.Unlock(); return w.active }
func (w *Wheel) Remove(it *Item) { w.mu.Lock(); defer w.mu.Unlock(); w.removeLocked(it) }

func (w *Wheel) Insert(it *Item) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.insertAt(it, w.curTick)
}

func (w *Wheel) insertAt(it *Item, cur int64) bool {
	k := ceilDiv(it.Deadline, w.tick)
	if k < cur {
		return false
	}
	w.active++
	if k >= cur+w.span(w.levels) {
		it.inHeap = true
		heap.Push(&w.over, it)
		return true
	}
	for lv := range w.levels {
		if sp := w.span(lv); k < cur+sp*int64(w.slotsN) || lv == w.levels-1 {
			idx := int(k/sp) & (w.slotsN - 1)
			it.elem = w.rings[lv].AddOrdered(idx, it, itemLess)
			it.level, it.slotIdx = lv, idx
			w.nonEmpty[lv][idx] = struct{}{}
			return true
		}
	}
	return true
}

func (w *Wheel) removeLocked(it *Item) {
	if !it.inHeap && it.elem == nil {
		return
	}
	w.active--
	if it.inHeap {
		heap.Remove(&w.over, it.heapIdx)
		it.inHeap = false
		return
	}
	lv, idx := it.level, it.slotIdx
	w.rings[lv].Remove(it.elem)
	it.elem = nil
	if w.rings[lv].Len(idx) == 0 {
		delete(w.nonEmpty[lv], idx)
	}
}

type FireFn func(it *Item) bool

func (w *Wheel) Advance(now int64, fire FireFn) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.visited = 0
	target := ceilDiv(now, w.tick) - 1
	if target < w.curTick {
		return
	}
	cur := w.curTick
	for cur <= target {
		for w.over.Len() > 0 && ceilDiv(w.over[0].Deadline, w.tick) <= cur+w.span(w.levels) {
			it := heap.Pop(&w.over).(*Item)
			it.inHeap = false
			w.insertAt(it, cur)
		}
		for lv := w.levels - 1; lv >= 0; lv-- {
			sp := w.span(lv)
			if lv > 0 && cur%sp != 0 {
				continue
			}
			idx := int(cur/sp) & (w.slotsN - 1)
			if _, ok := w.nonEmpty[lv][idx]; !ok {
				continue
			}
			w.visited++
			ch := w.rings[lv].Detach(idx)
			delete(w.nonEmpty[lv], idx)
			if lv > 0 {
				w.cascade(ch, lv, cur)
				continue
			}
			for e := ch.Head; e != nil; e = ch.Head {
				ch.Head = e.Next()
				it := e.Value.(*Item)
				it.elem = nil
				if fire(it) {
					w.active--
				}
			}
		}
		cur = w.nextEvent(cur+1, target)
	}
	w.curTick = cur
}

// nextEvent 跳到下一非空 0 层槽、非空层边界或溢出点;无则 target+1。
func (w *Wheel) nextEvent(from, target int64) int64 {
	if from > target {
		return from
	}
	next := target + 1
	consider := func(k int64) {
		if from <= k && k <= target && k < next {
			next = k
		}
	}
	for idx := range w.nonEmpty[0] {
		consider(from + (int64(idx)-from)&(int64(w.slotsN)-1))
	}
	for lv := 1; lv < w.levels; lv++ {
		sp := w.span(lv)
		b := ((from + sp - 1) / sp) * sp
		idx := int(b/sp) & (w.slotsN - 1)
		if _, ok := w.nonEmpty[lv][idx]; ok {
			consider(b)
		}
	}
	if w.over.Len() > 0 {
		consider(ceilDiv(w.over[0].Deadline, w.tick))
	}
	return next
}

func (w *Wheel) cascade(c slot.Chain, fromLevel int, cur int64) {
	for e := c.Head; e != nil; e = e.Next() {
		it := e.Value.(*Item)
		k := ceilDiv(it.Deadline, w.tick)
		lv := 0
		for l := fromLevel - 1; l >= 1; l-- {
			if k >= cur+w.span(l)*int64(w.slotsN) {
				lv, l = l, 0
			}
		}
		idx := int(k/w.span(lv)) & (w.slotsN - 1)
		it.elem = w.rings[lv].AddOrdered(idx, it, itemLess)
		it.level, it.slotIdx = lv, idx
		w.nonEmpty[lv][idx] = struct{}{}
	}
}

type overHeap []*Item

func (h overHeap) Len() int           { return len(h) }
func (h overHeap) Less(i, j int) bool { return itemLess(h[i], h[j]) }
func (h overHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heapIdx, h[j].heapIdx = i, j
}
func (h *overHeap) Push(x any) {
	it := x.(*Item)
	it.heapIdx = len(*h)
	*h = append(*h, it)
}
func (h *overHeap) Pop() any {
	old := *h
	it := old[len(old)-1]
	*h = old[:len(old)-1]
	return it
}
