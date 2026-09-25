// Package wagg 按 (Key, 窗口) 维护计数、水位线推进、懒创建、触发与变更日志。
package wagg

import (
	"container/heap"
	"errors"

	"ontology/win"
)

// 三类可判定哨兵错误，互不相同。
var (
	ErrParam = errors.New("wagg: invalid size/hop")
	ErrDelay = errors.New("wagg: negative delay")
	ErrOrder = errors.New("wagg: out-of-order TS")
)

// Event 是一条带事件时间的变更。
type Event struct {
	Key string
	TS  int64
}

// Change 是窗口触发时输出的变更日志条目：+(Key, [Start,End), Count)。
type Change struct {
	Key        string
	Start, End int64
	Count      int64
}

type wkey struct {
	key   string
	start int64
}

// item 是待触发窗口在最小堆中的引用（按 end 升序弹出）。
type item struct {
	key        string
	start, end int64
}

type pq []item

func (p pq) Len() int           { return len(p) }
func (p pq) Less(i, j int) bool { return p[i].end < p[j].end }
func (p pq) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }
func (p *pq) Push(x any)        { *p = append(*p, x.(item)) }
func (p *pq) Pop() (x any)      { old := *p; *p = old[:len(old)-1]; return old[len(old)-1] }

// Agg 维护全部未触发窗口与计数，状态只在进程内存。
type Agg struct {
	size, hop, delay int64
	maxTS            int64
	seen             bool
	counts           map[wkey]int64
	pend             pq
	lastChecks       int // 最近一次水位线推进时为判定触发检查过的窗口数
}

// New 校验参数并创建 Agg；参数非法时返回哨兵错误，不产生任何状态。
func New(size, hop, delay int64) (*Agg, error) {
	if size <= 0 || hop <= 0 || hop > size || size%hop != 0 {
		return nil, ErrParam
	}
	if delay < 0 {
		return nil, ErrDelay
	}
	return &Agg{size: size, hop: hop, delay: delay, counts: map[wkey]int64{}}, nil
}

// Feed 应用一批事件，返回本批触发的变更日志；任一事件乱序则整批不生效。
func (a *Agg) Feed(evs []Event) ([]Change, error) {
	prev, ok := a.maxTS, a.seen // 先整批校验，失败不留痕
	for _, e := range evs {
		if ok && e.TS < prev {
			return nil, ErrOrder
		}
		prev, ok = e.TS, true
	}
	var out []Change
	for _, e := range evs {
		out = append(out, a.feedOne(e)...)
	}
	return out, nil
}

func (a *Agg) feedOne(e Event) []Change {
	if !a.seen || e.TS > a.maxTS {
		a.maxTS, a.seen = e.TS, true
	}
	for _, w := range win.Windows(e.TS, a.size, a.hop) { // 懒创建
		k := wkey{e.Key, w.Start}
		if _, ok := a.counts[k]; !ok {
			heap.Push(&a.pend, item{e.Key, w.Start, w.End})
		}
		a.counts[k]++
	}
	return a.advance()
}

// advance 弹出所有 end <= wm 的窗口；只沿堆顶检查，不整表扫描。
func (a *Agg) advance() []Change {
	wm := a.maxTS - a.delay
	checks := 0
	var out []Change
	for len(a.pend) > 0 {
		checks++
		if !win.Fired(a.pend[0].end, wm) {
			break
		}
		it := heap.Pop(&a.pend).(item)
		out = append(out, a.fire(it))
	}
	a.lastChecks = checks
	return out
}

// Flush 把水位线提到正无穷，触发所有仍未触发的已创建窗口。
func (a *Agg) Flush() []Change {
	checks := 0
	var out []Change
	for len(a.pend) > 0 {
		checks++
		out = append(out, a.fire(heap.Pop(&a.pend).(item)))
	}
	a.lastChecks = checks
	return out
}

// fire 输出一条 + 变更并删除计数：每个 (Key,窗口) 恰好触发一次。
func (a *Agg) fire(it item) Change {
	k := wkey{it.key, it.start}
	c := Change{Key: it.key, Start: it.start, End: it.end, Count: a.counts[k]}
	delete(a.counts, k)
	return c
}
