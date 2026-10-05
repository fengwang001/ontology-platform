// Package labelpool 管理标签区间内的空闲、隔离与亲和状态。
//
// 不变式：任一时刻一个标签至多处于一种状态（已分配/空闲/隔离）；
// 隔离中的标签只能被其释放者以亲和方式取回，到期（释放时刻+Hd，恰等即到期）
// 后转入空闲并丧失亲和。
package labelpool

import "errors"

const (
	MinLabel = 16
	MaxLabel = 1048575
	MaxHold  = int64(1_000_000_000)
)

var (
	ErrInvalid = errors.New("labelpool: invalid argument")
	ErrCorrupt = errors.New("labelpool: inconsistent state")
)

// labelHeap 是按标签排序、支持任意删除的索引最小堆。
type labelHeap struct {
	items []int
	pos   map[int]int
}

func newLabelHeap(capHint int) *labelHeap {
	return &labelHeap{items: make([]int, 0, capHint), pos: make(map[int]int, capHint)}
}

func (h *labelHeap) len() int  { return len(h.items) }
func (h *labelHeap) peek() int { return h.items[0] }

func (h *labelHeap) push(x int) {
	h.pos[x] = len(h.items)
	h.items = append(h.items, x)
	h.siftUp(len(h.items) - 1)
}

func (h *labelHeap) siftUp(i int) {
	for i > 0 {
		p := (i - 1) / 2
		if h.items[p] <= h.items[i] {
			break
		}
		h.swap(i, p)
		i = p
	}
}

func (h *labelHeap) siftDown(i int) {
	for {
		l, r := 2*i+1, 2*i+2
		m := i
		if l < len(h.items) && h.items[l] < h.items[m] {
			m = l
		}
		if r < len(h.items) && h.items[r] < h.items[m] {
			m = r
		}
		if m == i {
			return
		}
		h.swap(i, m)
		i = m
	}
}

func (h *labelHeap) pop() int {
	top := h.items[0]
	h.removeAt(0)
	return top
}

func (h *labelHeap) remove(x int) {
	if i, ok := h.pos[x]; ok {
		h.removeAt(i)
	}
}

func (h *labelHeap) removeAt(i int) {
	last := len(h.items) - 1
	removed := h.items[i]
	h.swap(i, last)
	h.items = h.items[:last]
	delete(h.pos, removed)
	if i < last {
		h.siftDown(i)
		h.siftUp(i)
	}
}

func (h *labelHeap) swap(i, j int) {
	h.items[i], h.items[j] = h.items[j], h.items[i]
	h.pos[h.items[i]] = i
	h.pos[h.items[j]] = j
}

// qItem 是隔离堆元素，按 (释放时刻, 标签) 排序。
type qItem struct {
	release int64
	label   int
}

// quarHeap 是隔离标签的索引最小堆，支持按标签任意删除。
type quarHeap struct {
	items []qItem
	pos   map[int]int
}

func newQuarHeap() *quarHeap {
	return &quarHeap{pos: make(map[int]int)}
}

func quarLess(a, b qItem) bool {
	if a.release != b.release {
		return a.release < b.release
	}
	return a.label < b.label
}

func (h *quarHeap) len() int    { return len(h.items) }
func (h *quarHeap) peek() qItem { return h.items[0] }

func (h *quarHeap) push(it qItem) {
	h.pos[it.label] = len(h.items)
	h.items = append(h.items, it)
	h.siftUp(len(h.items) - 1)
}

func (h *quarHeap) pop() qItem {
	top := h.items[0]
	h.removeAt(0)
	return top
}

func (h *quarHeap) remove(label int) {
	if i, ok := h.pos[label]; ok {
		h.removeAt(i)
	}
}

func (h *quarHeap) removeAt(i int) {
	last := len(h.items) - 1
	removed := h.items[i]
	h.swap(i, last)
	h.items = h.items[:last]
	delete(h.pos, removed.label)
	if i < last {
		h.siftDown(i)
		h.siftUp(i)
	}
}

func (h *quarHeap) siftUp(i int) {
	for i > 0 {
		p := (i - 1) / 2
		if !quarLess(h.items[i], h.items[p]) {
			break
		}
		h.swap(i, p)
		i = p
	}
}

func (h *quarHeap) siftDown(i int) {
	for {
		l, r := 2*i+1, 2*i+2
		m := i
		if l < len(h.items) && quarLess(h.items[l], h.items[m]) {
			m = l
		}
		if r < len(h.items) && quarLess(h.items[r], h.items[m]) {
			m = r
		}
		if m == i {
			return
		}
		h.swap(i, m)
		i = m
	}
}

func (h *quarHeap) swap(i, j int) {
	h.items[i], h.items[j] = h.items[j], h.items[i]
	h.pos[h.items[i].label] = i
	h.pos[h.items[j].label] = j
}

type quarEntry struct {
	release int64
	fec     string
}

// Pool 管理 [lo,hi] 标签区间。非并发安全，由上层串行化。
type Pool struct {
	lo, hi    int
	hd        int64
	allocated map[int]string
	freeSet   map[int]struct{}
	freeHeap  *labelHeap
	quar      map[int]quarEntry
	quarHeap  *quarHeap
	affinity  map[string]int
	probes    int // 最近一次 Alloc 考察的标签数（非导出计数器）
}

// New 创建标签池：16≤lo≤hi≤1048575，0≤hd≤1e9 毫秒。
func New(lo, hi int, hd int64) (*Pool, error) {
	if lo < MinLabel || hi > MaxLabel || lo > hi || hd < 0 || hd > MaxHold {
		return nil, ErrInvalid
	}
	n := hi - lo + 1
	p := &Pool{
		lo:        lo,
		hi:        hi,
		hd:        hd,
		allocated: make(map[int]string),
		freeSet:   make(map[int]struct{}, n),
		freeHeap:  newLabelHeap(n),
		quar:      make(map[int]quarEntry),
		quarHeap:  newQuarHeap(),
		affinity:  make(map[string]int),
	}
	for l := lo; l <= hi; l++ {
		p.freeSet[l] = struct{}{}
		p.freeHeap.push(l)
	}
	return p, nil
}

// Lo 返回区间下界。
func (p *Pool) Lo() int { return p.lo }

// Hi 返回区间上界。
func (p *Pool) Hi() int { return p.hi }

// CanAlloc 报告 now 时刻 fec 是否一定能取到标签（不修改任何状态）。
func (p *Pool) CanAlloc(fec string, now int64) bool {
	if l, ok := p.affinity[fec]; ok {
		if e, q := p.quar[l]; q && e.release+p.hd > now {
			return true
		}
	}
	if len(p.freeSet) > 0 {
		return true
	}
	if p.quarHeap.len() > 0 && p.quarHeap.peek().release+p.hd <= now {
		return true
	}
	return false
}

// land 把隔离到期的标签迁入空闲，计入 probes。
func (p *Pool) land(now int64) {
	for p.quarHeap.len() > 0 {
		top := p.quarHeap.peek()
		if top.release+p.hd > now {
			break
		}
		p.quarHeap.pop()
		e := p.quar[top.label]
		delete(p.quar, top.label)
		if p.affinity[e.fec] == top.label {
			delete(p.affinity, e.fec)
		}
		p.freeSet[top.label] = struct{}{}
		p.freeHeap.push(top.label)
		p.probes++
	}
}

// Alloc 为 fec 分配标签：亲和优先，否则取最小可用标签。
// 调用前应由 CanAlloc 或上层视图保证可取到。
func (p *Pool) Alloc(fec string, now int64) (int, bool) {
	p.probes = 0
	p.land(now)
	if l, ok := p.affinity[fec]; ok {
		if _, q := p.quar[l]; q {
			p.probes++
			p.quarHeap.remove(l)
			delete(p.quar, l)
			delete(p.affinity, fec)
			p.allocated[l] = fec
			return l, true
		}
		delete(p.affinity, fec)
	}
	if p.freeHeap.len() == 0 {
		return 0, false
	}
	p.probes++
	l := p.freeHeap.pop()
	delete(p.freeSet, l)
	p.allocated[l] = fec
	return l, true
}

// Free 释放 label，进入隔离期（释放时刻为 t），并记录 fec 的亲和。
func (p *Pool) Free(fec string, label int, t int64) {
	delete(p.allocated, label)
	p.quar[label] = quarEntry{release: t, fec: fec}
	p.quarHeap.push(qItem{release: t, label: label})
	p.affinity[fec] = label
}

// AllocRaw 按日志重放把 label 直接分给 fec（Restore 专用）。
// label 必须处于空闲或隔离状态，否则报 ErrCorrupt。
func (p *Pool) AllocRaw(fec string, label int) error {
	if label < p.lo || label > p.hi {
		return ErrCorrupt
	}
	if _, ok := p.allocated[label]; ok {
		return ErrCorrupt
	}
	if e, ok := p.quar[label]; ok {
		p.quarHeap.remove(label)
		delete(p.quar, label)
		if p.affinity[e.fec] == label {
			delete(p.affinity, e.fec)
		}
	} else if _, ok := p.freeSet[label]; ok {
		delete(p.freeSet, label)
		p.freeHeap.remove(label)
	} else {
		return ErrCorrupt
	}
	p.allocated[label] = fec
	return nil
}
