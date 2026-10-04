// Package cmdq 维护每设备指令队列：未投递、待确认、过期、重投与失败。
package cmdq

import (
	"container/heap"
	"errors"
)

// 状态类错误（与 sched 中的哨兵错误为同一语义）。
var (
	ErrInvalid     = errors.New("cmdq: invalid parameters")
	ErrDupCmd      = errors.New("cmdq: duplicate command id")
	ErrTooBig      = errors.New("cmdq: command larger than byte budget")
	ErrUnreachable = errors.New("cmdq: command expires before next window")
	ErrFull        = errors.New("cmdq: device queue full")
	ErrNoCmd       = errors.New("cmdq: no such pending command")
)

// Limits 为每窗口额度与队列上限。
type Limits struct {
	K  int
	Bw int64
	R  int
	Q  int
}

// Stats 是一次 Deliver 的结果计数。
type Stats struct {
	Delivered []string
	Expired   []string
	Failed    []string
}

// Windower 回答当前窗口与下一个可用窗口的问题。
type Windower interface {
	WindowAt(now int64) (start int64, ok bool)
	NextAvail(t int64) int64
}

type cmd struct {
	id      string
	size    int64
	prio    int
	expire  int64
	seq     int
	sent    int
	win     int64 // 最后一次投递所在窗口起点
	first   int64 // 首次投递所在窗口起点
	pending bool
	expIdx  int // 过期堆中的位置
	pendIdx int // prio 堆中的位置
	eligIdx int // eligible 堆中的位置
}

// Queue 是一台设备的指令队列。
type Queue struct {
	lim      Limits
	seqGen   int
	cmds     map[string]*cmd
	pending  [4]seqHeap // prio 分堆，堆内 seq 升序
	expire   expHeap    // expire 升序
	eligible firstHeap  // 可重投待确认，first 升序
	winCmds  []*cmd     // 最近一次轮换窗口中投递的指令（追加序=首次投递序）
	curWin   int64      // 当前已轮换到的窗口起点
	winUsed  int        // 当前窗口已用条数
	winBytes int64      // 当前窗口已用字节
	examined int
}

// New 创建队列。
func New(lim Limits) *Queue {
	q := &Queue{lim: lim, curWin: -1}
	for i := range q.pending {
		q.pending[i].q = q
		heap.Init(&q.pending[i])
	}
	q.expire.q = q
	q.eligible.q = q
	heap.Init(&q.eligible)
	return q
}

// Examined 返回最近一次 BeginDeliver 真正考察的指令数。
func (q *Queue) Examined() int { return q.examined }

// Enqueue 入队一条指令；被拒绝时队列不发生任何变化。
// 检查次序：参数非法 > id 重复 > size>Bw > expire≤下一可用时刻 > 队列满。
func (q *Queue) Enqueue(id string, size int64, prio int, expire, now int64, w Windower) error {
	if id == "" || size < 1 || size > 1e6 || prio < 0 || prio > 3 || expire < 0 || now < 0 {
		return ErrInvalid
	}
	if _, ok := q.cmds[id]; ok {
		return ErrDupCmd
	}
	if size > q.lim.Bw {
		return ErrTooBig
	}
	if expire <= w.NextAvail(now) {
		return ErrUnreachable
	}
	live := 0
	for _, c := range q.cmds {
		if c.expire > now {
			live++
		}
	}
	if live >= q.lim.Q {
		return ErrFull
	}
	q.purge(now)
	c := &cmd{id: id, size: size, prio: prio, expire: expire, seq: q.seqGen,
		pending: true, expIdx: -1, pendIdx: -1, eligIdx: -1}
	q.seqGen++
	if q.cmds == nil {
		q.cmds = map[string]*cmd{}
	}
	q.cmds[c.id] = c
	heap.Push(&q.expire, c)
	heap.Push(&q.pending[c.prio], c)
	return nil
}

// BeginDeliver 执行一次窗口投递。winStart 必须是 now 所在窗口的起点。
// 步骤：清过期 → 判失败并换窗 → 组候选（重投在前、未投递在后）→
// 沿候选顺序消耗 K 条与 Bw 字节，首个放不下即停。
func (q *Queue) BeginDeliver(winStart, now int64) Stats {
	q.examined = 0
	var st Stats
	st.Expired = q.purge(now)
	q.rotate(winStart, &st)

	rem := q.lim.K - q.winUsed
	remBytes := q.lim.Bw - q.winBytes
	pPrio := 3
	peekPend := func() *cmd {
		for pPrio >= 0 {
			h := &q.pending[pPrio]
			for h.Len() > 0 {
				c := h.peek()
				if !c.pending { // 惰性清理，不计 examined
					heap.Pop(h)
					continue
				}
				return c
			}
			pPrio--
		}
		return nil
	}
	peekElig := func() *cmd {
		for q.eligible.Len() > 0 {
			c := q.eligible.peek()
			if c.eligIdx < 0 || c.pending {
				heap.Pop(&q.eligible)
				continue
			}
			return c
		}
		return nil
	}

	for rem > 0 {
		c := peekElig()
		fromElig := c != nil
		if c == nil {
			c = peekPend()
		}
		if c == nil {
			break
		}
		q.examined++
		if c.size > remBytes {
			break // 队首阻塞：不跳过它取后面更小的
		}
		if fromElig {
			heap.Pop(&q.eligible)
		} else {
			heap.Pop(&q.pending[pPrio])
		}
		c.pending = false
		c.eligIdx = -1
		if c.sent == 0 {
			c.first = winStart
		}
		c.sent++
		c.win = winStart
		rem--
		remBytes -= c.size
		q.winUsed++
		q.winBytes += c.size
		q.winCmds = append(q.winCmds, c)
		st.Delivered = append(st.Delivered, c.id)
	}
	return st
}

// rotate 处理窗口推进：旧窗口投递且达 R 次的记 Failed，其余变为可重投。
func (q *Queue) rotate(winStart int64, st *Stats) {
	if q.curWin == winStart {
		return
	}
	if q.curWin >= 0 {
		var failed []*cmd
		for _, c := range q.winCmds {
			if _, ok := q.cmds[c.id]; !ok {
				continue // 已 Ack 或过期
			}
			if c.sent >= q.lim.R {
				q.remove(c)
				failed = append(failed, c)
				continue
			}
			c.eligIdx = -2
			heap.Push(&q.eligible, c)
		}
		sortByFirst(failed)
		for _, c := range failed {
			st.Failed = append(st.Failed, c.id)
		}
	}
	q.winCmds = q.winCmds[:0]
	q.curWin = winStart
	q.winUsed = 0
	q.winBytes = 0
}

// Ack 确认一条待确认且未过期的指令。
func (q *Queue) Ack(id string, now int64) error {
	c, ok := q.cmds[id]
	if !ok {
		return ErrNoCmd
	}
	if now >= c.expire {
		return ErrNoCmd
	}
	if c.pending {
		return ErrNoCmd // 未投递
	}
	q.remove(c)
	return nil
}

// purge 清除 now 时刻已过期（now≥expire）的指令。
func (q *Queue) purge(now int64) []string {
	var ids []string
	for q.expire.Len() > 0 {
		c := q.expire.peek()
		if c.expire > now {
			break
		}
		heap.Pop(&q.expire)
		if _, ok := q.cmds[c.id]; !ok {
			continue
		}
		ids = append(ids, c.id)
		q.remove(c)
	}
	return ids
}

// remove 将指令从 id 表摘除；堆索引交由惰性清理处理。
func (q *Queue) remove(c *cmd) {
	delete(q.cmds, c.id)
	c.pending = false
	c.eligIdx = -1
}
