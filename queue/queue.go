// Package queue 是带可见性超时与死信的进程内工作队列。
package queue

import (
	"errors"
	"sync"

	"ontology/clock"
	"ontology/receipt"
)

// 哨兵错误，彼此可用 errors.Is 区分。
var (
	ErrStaleReceipt = errors.New("queue: stale receipt")
)

// State 是消息的生命周期状态。
type State int

const (
Visible State = iota
	Inflight
	Acked
	Dead
)

// Msg 是投递给消费者的消息视图。
type Msg struct {
	ID   int64
	Body string
}

// Stats 是守恒计数快照。
type Stats struct {
	Sent, Visible, Inflight, Acked, Dead, Deliveries int64
}

// Config 配置容量与死信阈值。
type Config struct {
	MaxReceives int64 // 每条消息最多投递次数
	MaxInflight int   // 在途消息上限
}

// Probe 是复杂度实测用的非导出计数器外视图。
type Probe struct{ TickScans, AckChecks, ExtendChecks int }

type message struct {
	id                    int64
	body                  string
	state                 State
	gen, receives, visAt  int64
	deadline              int64
	vIdx, dIdx            int
}

// Q 是并发安全的工作队列。
type Q struct {
	mu          sync.Mutex
	clk         *clock.Clock
	maxReceives int64
	maxInflight int
	msgs        map[int64]*message
	visible     []*message // 按 (visAt, id) 升序
	inflight    []*message // 按 deadline 升序
	nextID      int64
	deliveries  int64
	scanN       int
	ackN        int
	extN        int
}

// New 构造队列；时钟必须由调用方注入。
func New(clk *clock.Clock, cfg Config) *Q {
	return &Q{clk: clk, maxReceives: cfg.MaxReceives, maxInflight: cfg.MaxInflight,
		msgs: map[int64]*message{}}
}

// Send 入队一条消息，返回其 id。
func (q *Q) Send(body string) int64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.nextID++
	m := &message{id: q.nextID, body: body, state: Visible, visAt: q.clk.Now(),
		vIdx: -1, dIdx: -1}
	q.msgs[m.id] = m
	q.pushVisible(m)
	return m.id
}

// Receive 按 FIFO 取出一条可见消息；在途达上限或无可见消息时 ok=false 且零副作用。
func (q *Q) Receive(visibility int64) (Msg, receipt.Receipt, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.inflight) >= q.maxInflight || len(q.visible) == 0 {
		return Msg{}, receipt.Receipt{}, false
	}
	m := q.popVisible()
	m.state = Inflight
	m.gen++
	m.receives++
	q.deliveries++
	m.deadline = q.clk.Now() + visibility
	q.pushInflight(m)
	return Msg{m.id, m.body}, receipt.Receipt{MsgID: m.id, Generation: m.gen}, true
}

// Ack 确认当前世代的在途消息；过期收据零副作用并返回 ErrStaleReceipt。
func (q *Q) Ack(r receipt.Receipt) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.ackN++
	m := q.msgs[r.MsgID]
	if m == nil || m.state != Inflight || m.gen != r.Generation {
		return ErrStaleReceipt
	}
	m.state = Acked
	q.removeInflight(m)
	return nil
}

// Extend 从当前刻重算截止；过期收据语义同 Ack。
func (q *Q) Extend(r receipt.Receipt, d int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.extN++
	m := q.msgs[r.MsgID]
	if m == nil || m.state != Inflight || m.gen != r.Generation {
		return ErrStaleReceipt
	}
	m.deadline = q.clk.Now() + d
	q.fixInflight(m)
	return nil
}

// Tick 推进时钟一刻，并在新刻先统一处理到期迁移（顺序见 DESIGN.md 第 2 节）。
func (q *Q) Tick() {
	q.mu.Lock()
	defer q.mu.Unlock()
	now := q.clk.Tick()
	for len(q.inflight) > 0 {
		q.scanN++
		top := q.inflight[0]
		if top.deadline > now {
			return
		}
		q.popInflight()
		if top.receives >= q.maxReceives {
			top.state = Dead
			top.vIdx, top.dIdx = -1, -1
			continue
		}
		top.state = Visible
		top.visAt = now
		q.pushVisible(top)
	}
}

// DeadLetters 返回死信快照，按 id 升序。
func (q *Q) DeadLetters() []Msg {
	q.mu.Lock()
	defer q.mu.Unlock()
	var out []Msg
	for id := int64(1); id <= q.nextID; id++ {
		if m := q.msgs[id]; m.state == Dead {
			out = append(out, Msg{m.id, m.body})
		}
	}
	return out
}

// Stats 返回守恒计数。
func (q *Q) Stats() Stats {
	q.mu.Lock()
	defer q.mu.Unlock()
	s := Stats{Sent: q.nextID, Deliveries: q.deliveries}
	for _, m := range q.msgs {
		switch m.state {
		case Visible:
			s.Visible++
		case Inflight:
			s.Inflight++
		case Acked:
			s.Acked++
		case Dead:
			s.Dead++
		}
	}
	return s
}

// Probe 读出并清零非导出的检查计数器。
func (q *Q) Probe() Probe {
	q.mu.Lock()
	defer q.mu.Unlock()
	p := Probe{q.scanN, q.ackN, q.extN}
	q.scanN, q.ackN, q.extN = 0, 0, 0
	return p
}
