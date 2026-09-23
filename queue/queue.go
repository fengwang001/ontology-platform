// Package queue 是带可见性超时与死信的进程内工作队列，依赖 clock 与 receipt。
package queue

import (
	"container/heap"
	"errors"
	"sort"
	"sync"

	"ontology/clock"
	"ontology/receipt"
)

// ErrStaleReceipt 表示收据已过期：消息被重投、已确认或进死信。操作零副作用。
var ErrStaleReceipt = errors.New("queue: stale receipt")

// Msg 是队列里的消息体。
type Msg struct {
	ID   int64
	Body string
}

// Config 配置队列上限。MaxReceives 为每消息最多投递次数。
type Config struct{ MaxReceives, MaxInflight int }

// Stats 是某一刻的计数快照，满足 Visible+Inflight+Acked+Dead == Sent。
type Stats struct{ Sent, Visible, Inflight, Acked, Dead, Delivered int64 }

type state int

const (
	stVisible state = iota
	stInflight
	stAcked
	stDead
)

type entry struct {
	id                                              int64
	bodyStr                                         string
	gen, receives, deadline, becameVisible, heapIdx int64
	st                                              state
}

type ih []*entry

func (h ih) Len() int { return len(h) }
func (h ih) Less(i, j int) bool {
	if h[i].deadline != h[j].deadline {
		return h[i].deadline < h[j].deadline
	}
	return h[i].id < h[j].id
}
func (h ih) Swap(i, j int) { h[i], h[j] = h[j], h[i]; h[i].heapIdx, h[j].heapIdx = int64(i), int64(j) }
func (h *ih) Push(x any)   { e := x.(*entry); e.heapIdx = int64(len(*h)); *h = append(*h, e) }
func (h *ih) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

// Q 是工作队列，所有方法可并发使用。
type Q struct {
	clk                *clock.Clock
	maxR, maxIn        int
	mu                 sync.Mutex
	seq                int64
	msgs               map[int64]*entry
	vis                []*entry
	visHead            int
	in                 ih
	dead               []Msg
	tickProbe, opProbe int64
}

// New 构造队列。
func New(c *clock.Clock, cfg Config) *Q {
	return &Q{clk: c, maxR: cfg.MaxReceives, maxIn: cfg.MaxInflight, msgs: map[int64]*entry{}}
}

// Send 入队一条消息，返回其 id，消息即刻可见。
func (q *Q) Send(body string) int64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.seq++
	e := &entry{id: q.seq, bodyStr: body, st: stVisible, becameVisible: q.clk.Now()}
	q.msgs[e.id] = e
	q.vis = append(q.vis, e)
	return e.id
}

// Receive 取一条可见消息，visibility 为在途可见性刻数；达到在途上限或无消息时 ok=false。
func (q *Q) Receive(visibility int64) (Msg, receipt.Receipt, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.maxIn > 0 && len(q.in) >= q.maxIn {
		return Msg{}, receipt.Receipt{}, false
	}
	for q.visHead < len(q.vis) && q.vis[q.visHead].st != stVisible {
		q.visHead++
	}
	if q.visHead == len(q.vis) {
		return Msg{}, receipt.Receipt{}, false
	}
	e := q.vis[q.visHead]
	q.vis[q.visHead] = nil
	q.visHead++
	e.st = stInflight
	e.gen++
	e.receives++
	e.deadline = q.clk.Now() + visibility
	heap.Push(&q.in, e)
	return Msg{e.id, e.bodyStr}, receipt.New(e.id, e.gen), true
}

// Tick 推进到期处理：把 deadline <= 当前刻的在途消息重设可见或转死信。
func (q *Q) Tick() {
	q.mu.Lock()
	defer q.mu.Unlock()
	now := q.clk.Now()
	var back []*entry
	for q.in.Len() > 0 {
		q.tickProbe++
		if q.in[0].deadline > now {
			break
		}
		e := heap.Pop(&q.in).(*entry)
		if e.receives >= int64(q.maxR) {
			e.st = stDead
			q.dead = append(q.dead, Msg{e.id, e.bodyStr})
			continue
		}
		e.st = stVisible
		e.gen++
		e.becameVisible = now
		back = append(back, e)
	}
	sort.Slice(back, func(i, j int) bool { return back[i].id < back[j].id })
	q.vis = append(q.vis, back...)
}

func (q *Q) lookup(r receipt.Receipt) *entry {
	q.opProbe++
	e := q.msgs[r.ID]
	if e == nil || e.st != stInflight || e.gen != r.Gen {
		return nil
	}
	return e
}

// Ack 确认一条在途消息；过期收据返回 ErrStaleReceipt 且零副作用。
func (q *Q) Ack(r receipt.Receipt) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	e := q.lookup(r)
	if e == nil {
		return ErrStaleReceipt
	}
	heap.Remove(&q.in, int(e.heapIdx))
	e.st = stAcked
	return nil
}

// Extend 从当前起重算在途截止刻；过期收据返回 ErrStaleReceipt 且零副作用。
func (q *Q) Extend(r receipt.Receipt, d int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	e := q.lookup(r)
	if e == nil {
		return ErrStaleReceipt
	}
	e.deadline = q.clk.Now() + d
	heap.Fix(&q.in, int(e.heapIdx))
	return nil
}

// DeadLetters 返回死信快照（按进入顺序）。
func (q *Q) DeadLetters() []Msg {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Msg, len(q.dead))
	copy(out, q.dead)
	return out
}

// Stats 返回守恒计数快照。
func (q *Q) Stats() Stats {
	q.mu.Lock()
	defer q.mu.Unlock()
	s := Stats{Sent: int64(len(q.msgs)), Dead: int64(len(q.dead))}
	for _, e := range q.msgs {
		switch e.st {
		case stVisible:
			s.Visible++
		case stInflight:
			s.Inflight++
		case stAcked:
			s.Acked++
		}
		s.Delivered += e.receives
	}
	return s
}

// TickChecks 返回自上次 ResetProbes 起 Tick 比较堆顶的次数。
func (q *Q) TickChecks() int64 { q.mu.Lock(); defer q.mu.Unlock(); return q.tickProbe }

// OpChecks 返回 Ack/Extend 的收据检查次数（每次调用计一）。
func (q *Q) OpChecks() int64 { q.mu.Lock(); defer q.mu.Unlock(); return q.opProbe }

// ResetProbes 清零非导出检查计数器。
func (q *Q) ResetProbes() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.tickProbe, q.opProbe = 0, 0
}
