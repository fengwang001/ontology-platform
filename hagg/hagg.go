// Package hagg 按 (Key, 窗口) 维护计数、推进时钟、关闭窗口并有序输出。
// 关闭定位按窗口结束时间有序（堆顶），不整表扫描。
package hagg

import (
	"container/heap"
	"errors"

	"ontology/hop"
)

var (
	ErrEmptyKey  = errors.New("hagg: empty key")
	ErrClockBack = errors.New("hagg: clock regression")
	ErrMaxOpen   = errors.New("hagg: too many open windows")
)

// Result 是一个 (Key, 窗口) 的最终计数，每个 (Key, 窗口) 只输出一次。
type Result struct {
	Key        string
	Start, End int64
	Count      int64
}

type winKey struct {
	key   string
	start int64
}

type entry struct {
	key        string
	start, end int64
	count      int64
}

// entryHeap 按 (end, key, start) 升序。
type entryHeap []*entry

func (h entryHeap) Len() int { return len(h) }
func (h entryHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if a.end != b.end {
		return a.end < b.end
	}
	if a.key != b.key {
		return a.key < b.key
	}
	return a.start < b.start
}
func (h entryHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *entryHeap) Push(x any)   { *h = append(*h, x.(*entry)) }
func (h *entryHeap) Pop() (x any) { old := *h; x = old[len(old)-1]; *h = old[:len(old)-1]; return }

// Agg 是跳跃窗口计数器。不是并发安全的，由调用方（api 包）加锁。
type Agg struct {
	size, slide int64
	maxOpen     int
	clock       int64
	clockSet    bool
	open        map[winKey]*entry
	byEnd       entryHeap
	dropped     int64
	out         []Result
	checked     int // 最近一次 Advance/Flush 检查过的打开窗口个数
}

func New(size, slide int64, maxOpen int) *Agg {
	return &Agg{size: size, slide: slide, maxOpen: maxOpen, open: map[winKey]*entry{}}
}

// Add 把事件计入它所属的、尚未关闭的全部窗口；全部已关闭则丢弃并计丢弃数。
// 任何校验失败都不改变状态。
func (a *Agg) Add(key string, ts int64) error {
	if key == "" {
		return ErrEmptyKey
	}
	var targets []int64
	for _, s := range hop.Starts(ts, a.size, a.slide) {
		if !a.clockSet || s+a.size > a.clock {
			targets = append(targets, s)
		}
	}
	if len(targets) == 0 {
		a.dropped++
		return nil
	}
	fresh := 0
	for _, s := range targets {
		if _, ok := a.open[winKey{key, s}]; !ok {
			fresh++
		}
	}
	if len(a.open)+fresh > a.maxOpen {
		return ErrMaxOpen
	}
	for _, s := range targets {
		wk := winKey{key, s}
		if e, ok := a.open[wk]; ok {
			e.count++
		} else {
			e := &entry{key: key, start: s, end: s + a.size, count: 1}
			a.open[wk] = e
			heap.Push(&a.byEnd, e)
		}
	}
	return nil
}

// Advance 把时钟推进到 t 并关闭所有 end<=t 的窗口，按 (end, Key) 升序输出。
func (a *Agg) Advance(t int64) ([]Result, error) {
	if a.clockSet && t < a.clock {
		return nil, ErrClockBack
	}
	a.clock, a.clockSet = t, true
	return a.closeUpto(t), nil
}

// Flush 等价于把时钟推进到正无穷：关闭全部打开窗口。
func (a *Agg) Flush() []Result {
	a.clock, a.clockSet = 1<<62, true // 足够大，视为正无穷
	return a.closeUpto(1 << 62)
}

func (a *Agg) closeUpto(t int64) []Result {
	a.checked = 0
	var res []Result
	for len(a.byEnd) > 0 {
		top := a.byEnd[0]
		a.checked++
		if top.end > t {
			break
		}
		heap.Pop(&a.byEnd)
		delete(a.open, winKey{top.key, top.start})
		r := Result{Key: top.key, Start: top.start, End: top.end, Count: top.count}
		res = append(res, r)
		a.out = append(a.out, r)
	}
	return res
}

// Results 返回迄今输出的全部结果（副本）。
func (a *Agg) Results() []Result { return append([]Result(nil), a.out...) }

// Dropped 返回被丢弃的事件数。
func (a *Agg) Dropped() int64 { return a.dropped }
