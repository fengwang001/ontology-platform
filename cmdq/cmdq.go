// Package cmdq 维护单设备的下行指令队列：过期、重投、额度与失败判定。
package cmdq

import (
	"container/heap"
	"container/list"

	"ontology/wake"
)

// 队列状态类错误。
var (
	ErrTooBig      = errStr("cmdq: command size exceeds byte budget")
	ErrUnreachable = errStr("cmdq: command expires before next available window")
	ErrFull        = errStr("cmdq: device queue full")
	ErrDupCmd      = errStr("cmdq: duplicate command id")
	ErrNoCmd       = errStr("cmdq: no such actionable command")
)

type errStr string

func (e errStr) Error() string { return string(e) }

type Status int

const (
	StatusPending  Status = iota // 未投递
	StatusInflight               // 待确认
	StatusAcked
	StatusExpired
	StatusFailed
)

// Cmd 是一条指令对外可见的快照。
type Cmd struct {
	ID     string
	Size   int64
	Prio   int64
	Expire int64
	Seq    int64
	Status Status
	Sends  int64
}

type cmd struct {
	id     string
	size   int64
	prio   int64
	expire int64
	seq    int64
	st     Status
	sends  int64
	firstW int64 // 首次投递所在窗口起点
	lastW  int64 // 最后一次投递所在窗口起点

	expIdx int // 在过期堆中的下标，-1 表示不在堆中
	hi     int // 在未投递堆中的下标，-1 表示不在堆中
	le     *list.Element
}

// 按 expire 升序、seq 升序的小顶堆（未投递 ∪ 待确认）。
type expireHeap []*cmd

func (h expireHeap) Len() int { return len(h) }
func (h expireHeap) Less(i, j int) bool {
	if h[i].expire != h[j].expire {
		return h[i].expire < h[j].expire
	}
	return h[i].seq < h[j].seq
}
func (h expireHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].expIdx = i
	h[j].expIdx = j
}
func (h *expireHeap) Push(x any) {
	c := x.(*cmd)
	c.expIdx = len(*h)
	*h = append(*h, c)
}
func (h *expireHeap) Pop() any {
	old := *h
	n := len(old)
	c := old[n-1]
	old[n-1] = nil
	c.expIdx = -1
	*h = old[:n-1]
	return c
}

// 未投递指令：prio 降序、seq 升序。
type readyHeap []*cmd

func (h readyHeap) Len() int { return len(h) }
func (h readyHeap) Less(i, j int) bool {
	if h[i].prio != h[j].prio {
		return h[i].prio > h[j].prio
	}
	return h[i].seq < h[j].seq
}
func (h readyHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].hi = i
	h[j].hi = j
}
func (h *readyHeap) Push(x any) {
	c := x.(*cmd)
	c.hi = len(*h)
	*h = append(*h, c)
}
func (h *readyHeap) Pop() any {
	old := *h
	n := len(old)
	c := old[n-1]
	old[n-1] = nil
	c.hi = -1
	*h = old[:n-1]
	return c
}

// Queue 是单设备指令队列。
type Queue struct {
	dev  *wake.Device
	bw   int64
	qcap int64

	nextSeq int64
	active  int64 // 未投递 + 待确认条数
	byID    map[string]*cmd

	exps    expireHeap
	ready   readyHeap
	pending *list.List // 待确认，按 firstW 升序；同窗口投递顺序即 firstW,seq 升序
	bound   *list.Element

	curWindow int64 // 当前共享额度的窗口起点
	usedK     int64 // 该窗口已投递条数
	usedBw    int64 // 该窗口已投递字节
	lastExam  int   // 最近一次 Deliver 考察的指令数
}

// New 创建绑定到 wake.Device 的队列，bw 为字节额度，qcap 为队列上限。
func New(dev *wake.Device, bw, qcap int64) *Queue {
	q := &Queue{
		dev: dev, bw: bw, qcap: qcap,
		byID:    make(map[string]*cmd),
		pending: list.New(),
	}
	q.exps = expireHeap{}
	q.ready = readyHeap{}
	heap.Init(&q.exps)
	heap.Init(&q.ready)
	return q
}

// Active 返回当前未投递与待确认的指令条数。
func (q *Queue) Active() int { return int(q.active) }

// Snapshot 返回一条指令的状态；已了结指令仍可查到最终结局。
func (q *Queue) Snapshot(id string) (Cmd, bool) {
	c, ok := q.byID[id]
	if !ok {
		return Cmd{}, false
	}
	return Cmd{ID: c.id, Size: c.size, Prio: c.prio, Expire: c.expire,
		Seq: c.seq, Status: c.st, Sends: c.sends}, true
}

// removeActive 把指令从其所在的堆/链表中摘除，但保留 byID 记录最终结局。
func (q *Queue) removeActive(c *cmd) {
	switch c.st {
	case StatusPending:
		if c.hi >= 0 {
			heap.Remove(&q.ready, c.hi)
		}
	case StatusInflight:
		if c.le != nil {
			if q.bound == c.le {
				q.bound = c.le.Next()
			}
			q.pending.Remove(c.le)
			c.le = nil
		}
	}
	if c.expIdx >= 0 {
		heap.Remove(&q.exps, c.expIdx)
	}
	q.active--
}

// purgeExpired 清除 now>=expire 的全部未投递/待确认指令，返回被清除的 id（expire,seq 序）。
func (q *Queue) purgeExpired(now int64) []string {
	var gone []string
	for q.exps.Len() > 0 && q.exps[0].expire <= now {
		c := heap.Pop(&q.exps).(*cmd)
		switch c.st {
		case StatusPending:
			if c.hi >= 0 {
				heap.Remove(&q.ready, c.hi)
			}
		case StatusInflight:
			if c.le != nil {
				if q.bound == c.le {
					q.bound = c.le.Next()
				}
				q.pending.Remove(c.le)
				c.le = nil
			}
		}
		c.st = StatusExpired
		q.active--
		gone = append(gone, c.id)
	}
	return gone
}

// Enqueue 按接纳规则入队：ErrTooBig > ErrDupCmd > ErrUnreachable > ErrFull。
func (q *Queue) Enqueue(id string, size, prio, expire, now int64) error {
	if id == "" {
		return ErrNoCmd
	}
	if c, ok := q.byID[id]; ok && (c.st == StatusPending || c.st == StatusInflight) {
		return ErrDupCmd
	}
	if size > q.bw {
		return ErrTooBig
	}
	s := q.dev.NextAvailable(now)
	if expire <= s {
		return ErrUnreachable
	}
	// 接纳的最后一步才执行状态变更：清除过期。
	q.purgeExpired(now)
	if q.active >= q.qcap {
		return ErrFull
	}
	q.nextSeq++
	c := &cmd{
		id: id, size: size, prio: prio, expire: expire,
		seq: q.nextSeq, st: StatusPending, expIdx: -1, hi: -1,
	}
	q.byID[id] = c
	heap.Push(&q.exps, c)
	heap.Push(&q.ready, c)
	q.active++
	return nil
}

// Ack 确认一条待确认且未过期（now<expire）的指令。
func (q *Queue) Ack(id string, now int64) error {
	c, ok := q.byID[id]
	if !ok || c.st != StatusInflight {
		return ErrNoCmd
	}
	if now >= c.expire {
		return ErrNoCmd
	}
	q.removeActive(c)
	c.st = StatusAcked
	return nil
}

// Delivery 是一次投递的结果。
type Delivery struct {
	IDs      []string
	Expired  []string
	Failed   []string
	Examined int
}

// send 记录一次投递：次数加一、成为待确认并挂到链表尾部（即 firstW,seq 序）。
func (q *Queue) send(c *cmd, start int64) {
	if c.st == StatusPending {
		if c.hi >= 0 {
			heap.Remove(&q.ready, c.hi)
		}
		c.st = StatusInflight
		c.firstW = start
	}
	c.sends++
	c.lastW = start
	if c.le == nil {
		c.le = q.pending.PushBack(c)
	}
}

// Deliver 在起点为 start 的窗口内按 k 条、bw 字节额度投递，r 为投递次数上限。
func (q *Queue) Deliver(start, now, k, bw, r int64) Delivery {
	var res Delivery

	// ① 清除过期：每条都计入 examined。
	res.Expired = q.purgeExpired(now)
	res.Examined += len(res.Expired)

	// 新窗口：分界指针回到队首；窗口未变时保留分界（本窗口已投出的指令在分界之后）。
	if start != q.curWindow {
		q.curWindow = start
		q.usedK = 0
		q.usedBw = 0
		q.bound = q.pending.Front()
	}

	// ②+③ 重投段：分界之前的待确认指令，按首次投递先后逐条考察。
	blocked := false
	for q.bound != nil {
		le := q.bound
		c := le.Value.(*cmd)
		if c.lastW >= start {
			break // 后面的都是本窗口投递过的，不重投
		}
		res.Examined++
		if c.sends >= r {
			res.Failed = append(res.Failed, c.id)
			q.bound = le.Next()
			q.removeActive(c)
			c.st = StatusFailed
			continue
		}
		if q.usedK >= k || q.usedBw+c.size > bw {
			blocked = true
			break // 条数尽或第一条放不下：即停，不跳过它
		}
		q.bound = le.Next()
		q.send(c, start)
		res.IDs = append(res.IDs, c.id)
		q.usedBw += c.size
		q.usedK++
	}

	// ③+④ 新投段：未投递堆，按预算逐条取，放不下即停。
	for !blocked && q.ready.Len() > 0 && q.usedK < k {
		c := q.ready[0]
		res.Examined++
		if q.usedBw+c.size > bw {
			break
		}
		heap.Pop(&q.ready)
		q.send(c, start)
		res.IDs = append(res.IDs, c.id)
		q.usedBw += c.size
		q.usedK++
	}

	q.lastExam = res.Examined
	return res
}

// LastExamined 返回最近一次 Deliver 实际考察的指令数。
func (q *Queue) LastExamined() int { return q.lastExam }
