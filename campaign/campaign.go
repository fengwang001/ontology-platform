// Package campaign 实现固件 OTA 升级活动编排器：按 plan 的下一跳路径派发
// 设备，用 slot 限制在途名额，处理超时、失败退避与熔断中止。所有操作由
// 单互斥锁串行化，结果等价于某个串行顺序。
package campaign

import (
	"container/heap"
	"errors"
	"sync"

	"ontology/plan"
	"ontology/slot"
)

var (
	ErrInvalid     = errors.New("campaign: invalid argument")
	ErrClockBack   = errors.New("campaign: clock moved backwards")
	ErrUnknown     = errors.New("campaign: unknown device")
	ErrExists      = errors.New("campaign: device already exists")
	ErrNotInFlight = errors.New("campaign: device not in flight")
	ErrStale       = errors.New("campaign: stale token")
	ErrVersion     = errors.New("campaign: version mismatch")
	ErrAborted     = errors.New("campaign: campaign aborted")
)

const maxNow = int64(1_000_000_000_000)

// State 是设备的六种状态。
type State int

const (
	Pending State = iota
	InFlight
	Done
	Failed
	Skipped
	Cancelled
)

const numStates = 6

func (s State) String() string {
	switch s {
	case Pending:
		return "Pending"
	case InFlight:
		return "InFlight"
	case Done:
		return "Done"
	case Failed:
		return "Failed"
	case Skipped:
		return "Skipped"
	case Cancelled:
		return "Cancelled"
	}
	return "Unknown"
}

type device struct {
	id       string
	version  int
	state    State
	attempts int
	readyAt  int64
	hop      int
	tok      int64
	dl       int64
	tm       *timeoutEntry
}

// readyEntry 按 (readyAt, id 字节序) 升序组织就绪设备。
type readyEntry struct {
	readyAt int64
	id      string
	dev     *device
}

type readyHeap []readyEntry

func (h readyHeap) Len() int { return len(h) }
func (h readyHeap) Less(i, j int) bool {
	if h[i].readyAt != h[j].readyAt {
		return h[i].readyAt < h[j].readyAt
	}
	return h[i].id < h[j].id
}
func (h readyHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *readyHeap) Push(x any)   { *h = append(*h, x.(readyEntry)) }
func (h *readyHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

// timeoutEntry 按 (dl, id 字节序) 升序组织在途设备的截止时刻，带堆索引
// 以便 Report 落地时精确移除，保证堆中恰好是全部 InFlight 设备。
type timeoutEntry struct {
	dl  int64
	id  string
	dev *device
	idx int
}

type timeoutHeap []*timeoutEntry

func (h timeoutHeap) Len() int { return len(h) }
func (h timeoutHeap) Less(i, j int) bool {
	if h[i].dl != h[j].dl {
		return h[i].dl < h[j].dl
	}
	return h[i].id < h[j].id
}
func (h timeoutHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].idx = i
	h[j].idx = j
}
func (h *timeoutHeap) Push(x any) {
	e := x.(*timeoutEntry)
	e.idx = len(*h)
	*h = append(*h, e)
}
func (h *timeoutHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return e
}

// DispatchItem 是一次实际派发的结果：设备 id、下一跳版本 hop、令牌 tok。
type DispatchItem struct {
	ID  []byte
	Hop int
	Tok int64
}

// Campaign 是 OTA 升级活动编排器。
type Campaign struct {
	mu    sync.Mutex
	pl    *plan.Plan
	slots *slot.Slot
	r     int
	b, d  int64
	f     int

	devices map[string]*device
	ready   readyHeap
	tm      timeoutHeap

	tok     int64
	failed  int
	aborted bool
	maxNow  int64

	counts [numStates]int

	popReady   int
	popTimeout int
}

// New 构造活动编排器。T 为目标版本，M 为必经版本集合，C 为在途名额，
// R 为单跳尝试上限，B 为退避基数毫秒，D 为单跳时限毫秒，F 为熔断阈值。
func New(T int, M []int, C, R int, B, D int64, F int) (*Campaign, error) {
	p, err := plan.New(T, M)
	if err != nil {
		return nil, ErrInvalid
	}
	s, err := slot.New(C)
	if err != nil {
		return nil, ErrInvalid
	}
	if R < 1 || R > 10 || B < 1 || B > 1_000_000_000 ||
		D < 1 || D > 1_000_000_000 || F < 1 || F > 100_000 {
		return nil, ErrInvalid
	}
	return &Campaign{
		pl:      p,
		slots:   s,
		r:       R,
		b:       B,
		d:       D,
		f:       F,
		devices: make(map[string]*device),
	}, nil
}

func validNow(now int64) bool { return now >= 0 && now <= maxNow }

// AddDevice 加入一台设备。v>=T 记为 Skipped；活动中止后加入的记为
// Cancelled；否则为 Pending，attempts=0，readyAt=now。
func (c *Campaign) AddDevice(now int64, id []byte, v int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !validNow(now) || len(id) == 0 || v < 1 || v > 1_000_000 {
		return ErrInvalid
	}
	if now < c.maxNow {
		return ErrClockBack
	}
	key := string(id)
	if _, ok := c.devices[key]; ok {
		return ErrExists
	}
	c.settle(now)
	d := &device{id: key, version: v, readyAt: now}
	switch {
	case v >= c.pl.Target():
		d.state = Skipped
	case c.aborted:
		d.state = Cancelled
	default:
		d.state = Pending
		heap.Push(&c.ready, readyEntry{readyAt: now, id: key, dev: d})
	}
	c.devices[key] = d
	c.counts[d.state]++
	c.maxNow = now
	return nil
}

// Dispatch 从就绪设备中按 (readyAt, id) 升序派发至多 n 台且不超过空闲
// 名额，返回 (id, hop, tok) 清单，可为空。活动已中止时报 ErrAborted。
func (c *Campaign) Dispatch(now int64, n int) ([]DispatchItem, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !validNow(now) || n < 1 || n > 10_000 {
		return nil, ErrInvalid
	}
	if now < c.maxNow {
		return nil, ErrClockBack
	}
	if c.wouldAbort(now) {
		return nil, ErrAborted
	}
	c.settle(now)
	var items []DispatchItem
	for len(items) < n && c.slots.Free() > 0 && len(c.ready) > 0 &&
		c.ready[0].readyAt <= now {
		e := heap.Pop(&c.ready).(readyEntry)
		c.popReady++
		d := e.dev
		hop := c.pl.Next(d.version)
		c.tok++
		c.setState(d, InFlight)
		d.hop = hop
		d.tok = c.tok
		d.dl = now + c.d
		te := &timeoutEntry{dl: d.dl, id: d.id, dev: d}
		d.tm = te
		heap.Push(&c.tm, te)
		c.slots.Acquire()
		items = append(items, DispatchItem{ID: []byte(d.id), Hop: hop, Tok: d.tok})
	}
	c.maxNow = now
	return items, nil
}

// Report 上报一台在途设备的单跳结果。tok 须等于设备当前令牌；ok 为真时
// ver 须等于其 hop。
func (c *Campaign) Report(now int64, id []byte, tok int64, ver int, ok bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !validNow(now) || tok < 1 || (ok && (ver < 1 || ver > 1_000_000)) {
		return ErrInvalid
	}
	if now < c.maxNow {
		return ErrClockBack
	}
	d, found := c.devices[string(id)]
	if !found {
		return ErrUnknown
	}
	if d.state != InFlight || d.dl <= now {
		return ErrNotInFlight
	}
	if tok != d.tok {
		return ErrStale
	}
	if ok && ver != d.hop {
		return ErrVersion
	}
	c.settle(now)
	heap.Remove(&c.tm, d.tm.idx)
	d.tm = nil
	if ok {
		c.slots.Release()
		d.version = ver
		d.attempts = 0
		switch {
		case ver == c.pl.Target():
			c.setState(d, Done)
		case c.aborted:
			c.setState(d, Cancelled)
		default:
			d.readyAt = now
			c.setState(d, Pending)
			heap.Push(&c.ready, readyEntry{readyAt: now, id: d.id, dev: d})
		}
	} else {
		c.fail(d, now)
	}
	c.maxNow = now
	return nil
}

// settle 把所有 dl<=now 的在途设备按 (dl, id) 升序逐个按一次失败结算，
// 失败时刻取 dl。入口结算中途触发中止时，其余超时仍照常结算。
func (c *Campaign) settle(now int64) {
	for len(c.tm) > 0 && c.tm[0].dl <= now {
		te := heap.Pop(&c.tm).(*timeoutEntry)
		c.popTimeout++
		te.dev.tm = nil
		c.fail(te.dev, te.dl)
	}
}

// fail 按一次失败结算在途设备 d，失败时刻为 t：释放名额、attempts 加一，
// 达到 R 则 Failed 并可能触发熔断，否则按中止与否回到 Pending 或 Cancelled。
func (c *Campaign) fail(d *device, t int64) {
	c.slots.Release()
	d.attempts++
	switch {
	case d.attempts == c.r:
		c.setState(d, Failed)
		c.failed++
		if c.failed == c.f {
			c.abort()
		}
	case c.aborted:
		c.setState(d, Cancelled)
	default:
		d.readyAt = t + c.b*int64(d.attempts)
		c.setState(d, Pending)
		heap.Push(&c.ready, readyEntry{readyAt: d.readyAt, id: d.id, dev: d})
	}
}

// abort 熔断：全部 Pending 设备变为 Cancelled，就绪堆随之清空（堆中恰好
// 是全部 Pending，清空后不存在失效条目，保证弹出计数与设备总数无关）。
func (c *Campaign) abort() {
	c.aborted = true
	for _, d := range c.devices {
		if d.state == Pending {
			c.setState(d, Cancelled)
		}
	}
	c.ready = c.ready[:0]
}

// wouldAbort 只读预检：若在 now 时刻落地入口结算，活动是否已中止。
func (c *Campaign) wouldAbort(now int64) bool {
	if c.aborted {
		return true
	}
	newFailed := 0
	for _, te := range c.tm {
		if te.dl <= now && te.dev.attempts+1 == c.r {
			newFailed++
		}
	}
	return c.failed+newFailed >= c.f
}

func (c *Campaign) setState(d *device, s State) {
	c.counts[d.state]--
	d.state = s
	c.counts[s]++
}
