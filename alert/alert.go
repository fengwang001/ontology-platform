// Package alert 管理危急事件的建立、并入、升级与逾期落地。
package alert

import (
	"container/heap"
	"errors"
	"sort"
	"sync"

	"ontology/labrule"
)

// 拒绝原因，判定次序：参数非法 > 时钟回退 > 不存在 > 无资格 > 状态不符。
var (
	ErrInvalidParam = errors.New("alert: invalid parameter")
	ErrClockBack    = errors.New("alert: clock rollback")
	ErrNotFound     = errors.New("alert: not found")
	ErrNoQual       = errors.New("alert: no qualification")
	ErrBadState     = errors.New("alert: bad state")
)

const (
	// MaxClock 是时刻上界（分钟），下界为 0。
	MaxClock = 1_000_000_000
	// MaxValue 是结果值绝对值上界。
	MaxValue = 1_000_000_000
)

// Status 是事件的闭环状态。
type Status int

const (
	PendingNotify Status = iota
	PendingReadBack
	PendingAct
	Closed
)

func (s Status) String() string {
	switch s {
	case PendingNotify:
		return "pending-notify"
	case PendingReadBack:
		return "pending-readback"
	case PendingAct:
		return "pending-act"
	case Closed:
		return "closed"
	}
	return "unknown"
}

// Reading 是并入事件的一条危急结果。
type Reading struct {
	Now int64
	V   int64
}

// Event 是一个危急值闭环事件。
type Event struct {
	ID         int
	Patient    string
	Code       string
	Sev        int
	Rep        int64
	Deadline   int64
	Status     Status
	Readings   []Reading
	Tech       string
	Receiver   string
	Mismatch   int
	Late       bool
	ClosedLate bool

	hidx int
}

// eventHeap 是按 (Deadline, ID) 升序的最小堆，只含未落地逾期的未闭环事件。
type eventHeap []*Event

func (h eventHeap) Len() int { return len(h) }

func (h eventHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if a.Deadline != b.Deadline {
		return a.Deadline < b.Deadline
	}
	return a.ID < b.ID
}

func (h eventHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].hidx = i
	h[j].hidx = j
}

func (h *eventHeap) Push(x any) {
	ev := x.(*Event)
	ev.hidx = len(*h)
	*h = append(*h, ev)
}

func (h *eventHeap) Pop() any {
	old := *h
	n := len(old)
	ev := old[n-1]
	old[n-1] = nil
	ev.hidx = -1
	*h = old[:n-1]
	return ev
}

// Center 持有危急值闭环的全部易变状态。所有方法可并发调用，
// 效果等价于某个串行顺序。
type Center struct {
	mu      sync.Mutex
	book    *labrule.Book
	T       [4]int64
	wards   map[string]string
	events  map[int]*Event
	open    map[string]*Event
	due     eventHeap
	nextID  int
	maxNow  int64
	started bool
	overdue []int

	touchedLocate int
	touchedLand   int
}

// New 创建一个闭环中心，T1/T2/T3 为三档严重度各自的闭环时限（分钟）。
func New(t1, t2, t3 int64) (*Center, error) {
	for _, d := range [3]int64{t1, t2, t3} {
		if d < 1 || d > 10_000 {
			return nil, ErrInvalidParam
		}
	}
	return &Center{
		book:   labrule.NewBook(),
		T:      [4]int64{0, t1, t2, t3},
		wards:  make(map[string]string),
		events: make(map[int]*Event),
		open:   make(map[string]*Event),
		nextID: 1,
	}, nil
}

// AddTest 注册检验项目的阈值规则。
func (c *Center) AddTest(code string, low, high, step int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.book.Add(code, low, high, step); err != nil {
		return ErrInvalidParam
	}
	return nil
}

// SetWard 设置患者当前病区；患者借此注册，未注册患者的 Result 报不存在。
func (c *Center) SetWard(patient, ward string) error {
	if patient == "" || ward == "" {
		return ErrInvalidParam
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.wards[patient] = ward
	return nil
}

// Result 上报一条检验结果。非危急返回 critical=false 且不触碰任何事件；
// 危急时按规则新建、并入或升级事件，返回事件号。
func (c *Center) Result(now int64, patient, code string, v int64) (id int, critical bool, err error) {
	if patient == "" || code == "" || now < 0 || now > MaxClock || v < -MaxValue || v > MaxValue {
		return 0, false, ErrInvalidParam
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkClock(now); err != nil {
		return 0, false, err
	}
	if !c.book.Has(code) {
		return 0, false, ErrNotFound
	}
	if _, ok := c.wards[patient]; !ok {
		return 0, false, ErrNotFound
	}
	c.commit(now)
	sev, critical := c.book.Severity(code, v)
	if !critical {
		return 0, false, nil
	}
	key := patient + "\x00" + code
	ev := c.open[key]
	if ev == nil {
		ev = &Event{
			ID:       c.nextID,
			Patient:  patient,
			Code:     code,
			Sev:      sev,
			Rep:      v,
			Deadline: now + c.T[sev],
			Status:   PendingNotify,
			Readings: []Reading{{Now: now, V: v}},
			hidx:     -1,
		}
		c.nextID++
		c.events[ev.ID] = ev
		c.open[key] = ev
		heap.Push(&c.due, ev)
		return ev.ID, true, nil
	}
	c.touchedLocate++
	ev.Readings = append(ev.Readings, Reading{Now: now, V: v})
	if sev > ev.Sev {
		ev.Sev = sev
		ev.Rep = v
		ev.Status = PendingNotify
		ev.Tech = ""
		ev.Receiver = ""
		ev.Mismatch = 0
		if d := now + c.T[sev]; d < ev.Deadline {
			ev.Deadline = d
			if ev.hidx >= 0 {
				heap.Fix(&c.due, ev.hidx)
			}
		}
	}
	return ev.ID, true, nil
}

// Overdue 返回已落地逾期事件号清单（按落地次序，不重复）。
func (c *Center) Overdue() []int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]int, len(c.overdue))
	copy(out, c.overdue)
	return out
}

// Snapshot 按事件号升序返回全部事件的副本，供核对与测试。
func (c *Center) Snapshot() []Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Event, 0, len(c.events))
	for _, ev := range c.events {
		cp := *ev
		cp.Readings = append([]Reading(nil), ev.Readings...)
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// checkClock 校验时钟不回退，调用方须持锁。
func (c *Center) checkClock(now int64) error {
	if c.started && now < c.maxNow {
		return ErrClockBack
	}
	return nil
}

// commit 落地所有新逾期事件并推进时钟，调用方须持锁且已完成全部拒绝判定。
func (c *Center) commit(now int64) {
	c.land(now)
	c.maxNow = now
	c.started = true
}

// land 按 (Deadline, ID) 升序落地所有 now > Deadline 的未闭环事件。
func (c *Center) land(now int64) {
	for len(c.due) > 0 {
		top := c.due[0]
		c.touchedLand++
		if now <= top.Deadline {
			break
		}
		heap.Pop(&c.due)
		top.Late = true
		c.overdue = append(c.overdue, top.ID)
	}
}

// Lock 与 Unlock 供上层包（notify）在组合操作时持有同一把锁，
// 持锁期间只允许调用 *Locked 方法。
func (c *Center) Lock()   { c.mu.Lock() }
func (c *Center) Unlock() { c.mu.Unlock() }

// CheckClockLocked 校验时钟不回退。
func (c *Center) CheckClockLocked(now int64) error { return c.checkClock(now) }

// CommitLocked 落地新逾期事件并推进时钟。
func (c *Center) CommitLocked(now int64) { c.commit(now) }

// EventLocked 按事件号查找事件，不存在返回 nil。
func (c *Center) EventLocked(id int) *Event { return c.events[id] }

// WardLocked 返回患者当前病区。
func (c *Center) WardLocked(patient string) (string, bool) {
	w, ok := c.wards[patient]
	return w, ok
}

// CloseLocked 将事件从未闭环索引与到期堆中移除。
func (c *Center) CloseLocked(ev *Event) {
	delete(c.open, ev.Patient+"\x00"+ev.Code)
	if ev.hidx >= 0 {
		heap.Remove(&c.due, ev.hidx)
	}
}
