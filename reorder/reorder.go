// Package reorder 实现有界乱序重排缓冲：最小堆缓冲、级联发出、超时 flush、next 推进。
package reorder

import (
	"container/heap"
	"errors"

	"ontology/seq"
)

// ErrOverflow 缓冲已达 maxBuffered 时再收到缺口事件。
var ErrOverflow = errors.New("reorder: 缓冲溢出")

type entry struct {
	ev seq.Event
	at int64 // 缓冲时的逻辑时钟
}

// minHeap 按 Seq 组织的堆；cmp 统计堆元素间比较次数（定位最小 Seq 的代价）。
type minHeap struct {
	e   []entry
	cmp *int
}

func (h minHeap) Len() int { return len(h.e) }
func (h minHeap) Less(i, j int) bool {
	*h.cmp++
	return h.e[i].ev.Seq < h.e[j].ev.Seq
}
func (h minHeap) Swap(i, j int) { h.e[i], h.e[j] = h.e[j], h.e[i] }
func (h *minHeap) Push(x any)   { h.e = append(h.e, x.(entry)) }
func (h *minHeap) Pop() any {
	old := h.e
	n := len(old)
	it := old[n-1]
	h.e = old[:n-1]
	return it
}

// Core 是重排缓冲器核心。非并发安全，并发保护由上层 api 提供。
type Core struct {
	next, now, lastAdvance int64
	maxBuffered, timeout   int
	buf                    minHeap
	lost                   *seq.Lost
	emitted                []seq.Event
	cmp                    int // 非导出：最近一次 flush/级联中为定位最小 Seq 的比较次数
}

// New 构造 Core；参数须已由上层校验（maxBuffered>=1, timeout>=0）。
func New(maxBuffered, timeout int) *Core {
	c := &Core{next: 1, maxBuffered: maxBuffered, timeout: timeout, lost: seq.NewLost()}
	c.buf.cmp = &c.cmp
	return c
}

// Feed 处理一个到达事件，返回本次发出的事件序列。
// 所有校验先于任何状态写，失败不留痕。
func (c *Core) Feed(ev seq.Event) ([]seq.Event, error) {
	if err := seq.Validate(ev.Seq); err != nil {
		return nil, err
	}
	switch seq.Classify(ev.Seq, c.next) {
	case seq.Stale:
		return nil, seq.ErrStale
	case seq.Hit:
		var out []seq.Event
		c.emit(ev, &out)
		c.cascade(&out)
		return out, nil
	default: // Gap：加入之前判超界，不留痕
		if len(c.buf.e) >= c.maxBuffered {
			return nil, ErrOverflow
		}
		heap.Push(&c.buf, entry{ev: ev, at: c.now})
		return nil, nil
	}
}

// Tick 推进逻辑时钟；缺口久等不来则超时 flush。
func (c *Core) Tick() ([]seq.Event, error) {
	c.now++
	var out []seq.Event
	if len(c.buf.e) > 0 && c.buf.e[0].ev.Seq > c.next && c.now-c.lastAdvance >= int64(c.timeout) {
		min := c.buf.e[0].ev.Seq // 堆顶即最小，0 次比较
		for s := c.next; s < min; s++ {
			c.lost.Add(s)
		}
		c.next = min
		c.cascade(&out) // 发出最小缓冲 Seq 并按级联规则继续
		c.lastAdvance = c.now
	}
	return out, nil
}

// emit 发出事件并推进 next；发出即不可回退。
func (c *Core) emit(ev seq.Event, out *[]seq.Event) {
	c.emitted = append(c.emitted, ev)
	*out = append(*out, ev)
	c.next++
	c.lastAdvance = c.now
}

// cascade 只要缓冲最小 Seq 恰等于 next 就弹出发出。
func (c *Core) cascade(out *[]seq.Event) {
	c.cmp = 0 // 只统计本次 flush/级联的比较代价
	for len(c.buf.e) > 0 && c.buf.e[0].ev.Seq == c.next {
		c.emit(heap.Pop(&c.buf).(entry).ev, out)
	}
}

// Now/Next 返回逻辑时钟与下一个待发出 Seq。
func (c *Core) Now() int64  { return c.now }
func (c *Core) Next() int64 { return c.next }

// Buffered 按 Seq 升序返回缓冲内容的副本。
func (c *Core) Buffered() []seq.Event {
	out := make([]seq.Event, len(c.buf.e))
	for i, en := range c.buf.e {
		out[i] = en.ev
	}
	for i := 1; i < len(out); i++ { // 插入排序，缓冲规模小
		for j := i; j > 0 && out[j].Seq < out[j-1].Seq; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// View 返回已发出事件序列的副本。
func (c *Core) View() []seq.Event { return append([]seq.Event(nil), c.emitted...) }

// Lost 按升序返回丢失 Seq 的副本。
func (c *Core) Lost() []int64 { return c.lost.List() }
