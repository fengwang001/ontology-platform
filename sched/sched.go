// Package sched 是面向低功耗休眠设备的下行指令排程器顶层入口。
//
// 所有操作在同一把互斥锁下串行化，因而天然线性化；拒绝路径不发生任何状态变更。
package sched

import (
	"errors"
	"sync"

	"ontology/cmdq"
	"ontology/wake"
)

// 调度器错误。
var (
	ErrInvalid     = errStr("sched: invalid argument")
	ErrClockBack   = errStr("sched: clock moved backwards")
	ErrNoDevice    = errStr("sched: device not found")
	ErrExists      = errStr("sched: device already registered")
	ErrAsleep      = errStr("sched: device is not in a receive window")
	ErrDupCmd      = errStr("sched: duplicate command id")
	ErrTooBig      = errStr("sched: command too big for byte budget")
	ErrUnreachable = errStr("sched: command expires before next available window")
	ErrFull        = errStr("sched: device queue full")
	ErrNoCmd       = errStr("sched: no such actionable command")
)

type errStr string

func (e errStr) Error() string { return string(e) }

// Params 是接收窗口参数的别名：窗口 [o+kP, o+kP+w)。
type Params = wake.Params

// Outcome 是指令的最终结局。
type Outcome int

const (
	OutQueued  Outcome = iota // 仍在队中（未投递或待确认）
	OutAcked                  // 已确认
	OutExpired                // 已过期
	OutFailed                 // 重投次数耗尽
)

// CmdInfo 是一条指令的对外快照。
type CmdInfo struct {
	Size   int64
	Prio   int64
	Expire int64
	Seq    int64
	Sends  int64
	Out    Outcome
}

type device struct {
	w *wake.Device
	q *cmdq.Queue
}

// Scheduler 管理全部设备的窗口、队列与全局额度。
type Scheduler struct {
	mu     sync.Mutex
	last   int64
	k, bw  int64
	r, cap int64
	devs   map[string]*device
}

// New 创建全局额度为 K 条/Bw 字节/R 次/Q 队列长的排程器。
func New(k, bw, r, qcap int64) *Scheduler {
	return &Scheduler{
		last: -1,
		k:    k, bw: bw, r: r, cap: qcap,
		devs: make(map[string]*device),
	}
}

func validParams(p Params) bool {
	return p.P >= 1 && p.P <= 1e9 &&
		p.O >= 0 && p.O < p.P &&
		p.W >= 1 && p.W <= p.P
}

func (s *Scheduler) lock()   { s.mu.Lock() }
func (s *Scheduler) unlock() { s.mu.Unlock() }

// Register 注册设备并给出初始窗口参数。
func (s *Scheduler) Register(dev string, p Params, now int64) error {
	if dev == "" || !validParams(p) || now < 0 || now > 1e12 {
		return ErrInvalid
	}
	s.lock()
	if now < s.last {
		s.unlock()
		return ErrClockBack
	}
	defer s.unlock()
	if _, ok := s.devs[dev]; ok {
		return ErrExists
	}
	s.last = now
	wd := wake.New(p)
	s.devs[dev] = &device{w: wd, q: cmdq.New(wd, s.bw, s.cap)}
	return nil
}

// Reconfigure 更换设备窗口参数，返回新参数生效时刻 e。
func (s *Scheduler) Reconfigure(dev string, p Params, now int64) (int64, error) {
	if dev == "" || !validParams(p) || now < 0 || now > 1e12 {
		return 0, ErrInvalid
	}
	s.lock()
	if now < s.last {
		s.unlock()
		return 0, ErrClockBack
	}
	defer s.unlock()
	d, ok := s.devs[dev]
	if !ok {
		return 0, ErrNoDevice
	}
	s.last = now
	return d.w.Reconfigure(p, now), nil
}

// Enqueue 向设备队列加入一条指令；id 在该设备未了结指令中唯一。
func (s *Scheduler) Enqueue(dev, id string, size, prio, expire, now int64) error {
	if dev == "" || id == "" || size < 1 || size > 1e6 ||
		prio < 0 || prio > 3 || now < 0 || now > 1e12 || expire < 0 || expire > 1e12 {
		return ErrInvalid
	}
	s.lock()
	if now < s.last {
		s.unlock()
		return ErrClockBack
	}
	defer s.unlock()
	d, ok := s.devs[dev]
	if !ok {
		return ErrNoDevice
	}
	if err := d.q.Enqueue(id, size, prio, expire, now); err != nil {
		return mapQErr(err)
	}
	s.last = now
	return nil
}

// Delivery 是一次醒来投递的结果。
type Delivery struct {
	IDs     []string
	Expired []string
	Failed  []string
}

// Deliver 由设备醒来时调用，now 必须落在某个接收窗口内。
func (s *Scheduler) Deliver(dev string, now int64) (Delivery, error) {
	if dev == "" || now < 0 || now > 1e12 {
		return Delivery{}, ErrInvalid
	}
	s.lock()
	if now < s.last {
		s.unlock()
		return Delivery{}, ErrClockBack
	}
	defer s.unlock()
	d, ok := s.devs[dev]
	if !ok {
		return Delivery{}, ErrNoDevice
	}
	start, in := d.w.WindowStart(now)
	if !in {
		return Delivery{}, ErrAsleep
	}
	s.last = now
	r := d.q.Deliver(start, now, s.k, s.bw, s.r)
	return Delivery{IDs: r.IDs, Expired: r.Expired, Failed: r.Failed}, nil
}

// Ack 任何时刻可调：指令须待确认且未过期。
func (s *Scheduler) Ack(dev, id string, now int64) error {
	if dev == "" || id == "" || now < 0 || now > 1e12 {
		return ErrInvalid
	}
	s.lock()
	if now < s.last {
		s.unlock()
		return ErrClockBack
	}
	defer s.unlock()
	d, ok := s.devs[dev]
	if !ok {
		return ErrNoDevice
	}
	if err := d.q.Ack(id, now); err != nil {
		return mapQErr(err)
	}
	s.last = now
	return nil
}

// Lookup 返回一条指令的快照（已了结指令保留最终结局）。
func (s *Scheduler) Lookup(dev, id string) (CmdInfo, bool) {
	s.lock()
	defer s.unlock()
	d, ok := s.devs[dev]
	if !ok {
		return CmdInfo{}, false
	}
	c, ok := d.q.Snapshot(id)
	if !ok {
		return CmdInfo{}, false
	}
	out := OutQueued
	switch c.Status {
	case cmdq.StatusAcked:
		out = OutAcked
	case cmdq.StatusExpired:
		out = OutExpired
	case cmdq.StatusFailed:
		out = OutFailed
	}
	return CmdInfo{Size: c.Size, Prio: c.Prio, Expire: c.Expire,
		Seq: c.Seq, Sends: c.Sends, Out: out}, true
}

// Examined 返回该设备最近一次 Deliver 的考察指令数（测试与观测用）。
func (s *Scheduler) Examined(dev string) (int, bool) {
	s.lock()
	defer s.unlock()
	d, ok := s.devs[dev]
	if !ok {
		return 0, false
	}
	return d.q.LastExamined(), true
}

func mapQErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, cmdq.ErrDupCmd):
		return ErrDupCmd
	case errors.Is(err, cmdq.ErrTooBig):
		return ErrTooBig
	case errors.Is(err, cmdq.ErrUnreachable):
		return ErrUnreachable
	case errors.Is(err, cmdq.ErrFull):
		return ErrFull
	default:
		return ErrNoCmd
	}
}
