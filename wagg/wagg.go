// Package wagg 按 (Key, 窗口) 维护计数、水位线、触发与清除，产出变更日志。
package wagg

import (
	"container/heap"
	"errors"
	"math"
	"ontology/win"
)

var (
	ErrParam    = errors.New("wagg: invalid parameter")
	ErrMaxOpen  = errors.New("wagg: too many open windows")
	ErrEmptyKey = errors.New("wagg: empty key")
)

// Event 是带事件时间的上游变更；Change 是一条 +(Key,[start,end),count) 或 -(...) 日志。
type Event struct {
	Key string
	TS  int64
}
type Change struct {
	Key               string
	Start, End, Count int64
	Retract           bool
}
type winKey struct {
	key   string
	start int64
}
type winState struct{ count, emitted int64 } // emitted>0 ⇔ 窗口已触发
type winHeap []winKey                        // 未清除窗口按 start（等价于按 end）有序的小根堆

func (h winHeap) Len() int           { return len(h) }
func (h winHeap) Less(i, j int) bool { return h[i].start < h[j].start }
func (h winHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *winHeap) Push(x any)        { *h = append(*h, x.(winKey)) }
func (h *winHeap) Pop() (top any)    { top = (*h)[len(*h)-1]; *h = (*h)[:len(*h)-1]; return }

// Aggregator 维护全部未清除窗口；checked 记录最近一次水位线推进时检查过的未清除窗口个数（非导出）。
type Aggregator struct {
	size, delay, lateness, wm, dropped int64
	maxOpen, checked                   int
	seen                               bool
	states                             map[winKey]*winState
	open                               winHeap
}

// New：size<=0 或 delay/lateness<0 返回 ErrParam；maxOpen<=0 表示不限。
func New(size, delay, lateness int64, maxOpen int) (*Aggregator, error) {
	if size <= 0 || delay < 0 || lateness < 0 {
		return nil, ErrParam
	}
	return &Aggregator{size: size, delay: delay, lateness: lateness, maxOpen: maxOpen,
		wm: math.MinInt64, states: map[winKey]*winState{}}, nil
}
func (a *Aggregator) Watermark() int64 { return a.wm }      // 未见过事件时为 MinInt64（负无穷）
func (a *Aggregator) Dropped() int64   { return a.dropped } // 被丢弃的迟到事件总数
func (a *Aggregator) clone() *Aggregator {
	c := *a
	c.states = make(map[winKey]*winState, len(a.states))
	for k, v := range a.states {
		s := *v
		c.states[k] = &s
	}
	c.open = append(winHeap(nil), a.open...)
	return &c
}

// Feed 事务性地喂入一批事件：任一条被拒则整批不生效，状态完全不变。
func (a *Aggregator) Feed(evs []Event) ([]Change, error) {
	c := a.clone()
	var out []Change
	for _, ev := range evs {
		ch, err := c.feedOne(ev)
		if err != nil {
			return nil, err
		}
		out = append(out, ch...)
	}
	*a = *c
	return out, nil
}
func (a *Aggregator) feedOne(ev Event) ([]Change, error) {
	if ev.Key == "" {
		return nil, ErrEmptyKey
	}
	start, end := win.Bounds(ev.TS, a.size)
	wm := win.Watermark(ev.TS, a.delay)
	if a.seen && a.wm > wm {
		wm = a.wm
	}
	k := winKey{ev.Key, start}
	st := a.states[k]
	if !win.Acceptable(wm, end, a.lateness) { // 超迟到：丢弃，水位线照样推进
		a.wm, a.seen = wm, true
		a.dropped++
		return a.advance(), nil
	}
	if st == nil && a.maxOpen > 0 && len(a.states) >= a.maxOpen {
		return nil, ErrMaxOpen // 失败不留痕：此时尚未改动任何状态
	}
	a.wm, a.seen = wm, true
	out := a.advance()
	if st == nil {
		st = &winState{}
		a.states[k] = st
		heap.Push(&a.open, k)
	}
	st.count++
	if st.emitted > 0 || wm >= end { // 已触发（含新建即迟到）窗口：立即反映到输出
		if st.emitted > 0 { // 先 - 旧值再 + 新值
			out = append(out, Change{ev.Key, start, end, st.emitted, true})
		}
		st.emitted = st.count
		out = append(out, Change{ev.Key, start, end, st.count, false})
	}
	return out, nil
}

// advance 触发并清除到期窗口：按 end 有序定位堆顶不整表扫描；触发不弹堆，清除才弹堆。
func (a *Aggregator) advance() []Change {
	a.checked = 0
	var out []Change
	for len(a.open) > 0 {
		a.checked++
		top := a.open[0]
		end := top.start + a.size
		st := a.states[top]
		switch {
		case st.emitted == 0 && win.Triggered(a.wm, end):
			st.emitted = st.count
			out = append(out, Change{top.key, top.start, end, st.count, false})
		case win.Purgeable(a.wm, end, a.lateness):
			heap.Pop(&a.open)
			delete(a.states, top)
		default:
			return out
		}
	}
	return out
}

// Flush 把水位线推进到正无穷，触发并清除所有剩余窗口。
func (a *Aggregator) Flush() []Change {
	a.wm = math.MaxInt64
	return a.advance()
}
