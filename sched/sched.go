// Package sched 是下行指令排程器的唯一外观：设备注册、换参、入队、
// 窗口投递与确认。
package sched

import (
	"errors"
	"sync"

	"ontology/cmdq"
	"ontology/wake"
)

// 全部对外错误。拒绝次序：ErrInvalid > ErrClockBack > ErrNoDevice/
// ErrExists > ErrDupCmd > 各操作自身状态错误。
var (
	ErrInvalid     = errors.New("sched: invalid parameters")
	ErrClockBack   = errors.New("sched: clock moved backwards")
	ErrNoDevice    = errors.New("sched: device not registered")
	ErrExists      = errors.New("sched: device already registered")
	ErrDupCmd      = cmdq.ErrDupCmd
	ErrTooBig      = cmdq.ErrTooBig
	ErrUnreachable = cmdq.ErrUnreachable
	ErrFull        = cmdq.ErrFull
	ErrNoCmd       = cmdq.ErrNoCmd
	ErrAsleep      = errors.New("sched: device is outside a receive window")
)

const maxTime int64 = 1e12

type device struct {
	wake *wake.Device
	qq   *cmdq.Queue
	now  int64
	last Settled
}

// Scheduler 排程器。
type Scheduler struct {
	mu sync.Mutex

	k  int
	bw int64
	r  int
	q  int

	devs map[string]*device

	// examined 为最近一次 Deliver 真正考察的指令数。
	examined int
}

// New 创建排程器。K 每窗口条数、Bw 每窗口字节、R 投递次数上限、
// Q 每设备队列上限。
func New(K int, Bw int64, R, Q int) *Scheduler {
	return &Scheduler{k: K, bw: Bw, r: R, q: Q, devs: map[string]*device{}}
}

func validWin(P, o, w int64) bool {
	return P >= 1 && P <= 1e9 && o >= 0 && o < P && w >= 1 && w <= P
}

func validTime(now int64) bool { return now >= 0 && now <= maxTime }

// Register 注册设备。
func (s *Scheduler) Register(dev string, P, o, w, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validWin(P, o, w) || !validTime(now) {
		return ErrInvalid
	}
	if d, ok := s.devs[dev]; ok {
		if now < d.now {
			return ErrClockBack
		}
		return ErrExists
	}
	d := &device{
		wake: wake.New(wake.Params{P: P, O: o, W: w}),
		qq:   cmdq.New(cmdq.Limits{K: s.k, Bw: s.bw, R: s.r, Q: s.q}),
		now:  now,
	}
	s.devs[dev] = d
	return nil
}

// Reconfigure 更换设备窗口参数。
func (s *Scheduler) Reconfigure(dev string, P, o, w, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validWin(P, o, w) || !validTime(now) {
		return ErrInvalid
	}
	d, ok := s.devs[dev]
	if !ok {
		return ErrNoDevice
	}
	if now < d.now {
		return ErrClockBack
	}
	if err := d.wake.Reconfigure(wake.Params{P: P, O: o, W: w}, now); err != nil {
		return mapErr(err)
	}
	d.now = now
	return nil
}

// Enqueue 入队一条指令。
func (s *Scheduler) Enqueue(dev, id string, size int64, prio int, expire, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" || size < 1 || size > 1e6 || prio < 0 || prio > 3 ||
		!validTime(expire) || !validTime(now) {
		return ErrInvalid
	}
	d, ok := s.devs[dev]
	if !ok {
		return ErrNoDevice
	}
	if now < d.now {
		return ErrClockBack
	}
	if err := d.qq.Enqueue(id, size, prio, expire, now, d.wake); err != nil {
		return mapErr(err)
	}
	d.now = now
	return nil
}

// Deliver 在设备醒来的窗口内投递指令，返回投递的 id 清单。
func (s *Scheduler) Deliver(dev string, now int64) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validTime(now) {
		return nil, ErrInvalid
	}
	d, ok := s.devs[dev]
	if !ok {
		return nil, ErrNoDevice
	}
	if now < d.now {
		return nil, ErrClockBack
	}
	start, in := d.wake.WindowAt(now)
	if !in {
		return nil, ErrAsleep
	}
	st := d.qq.BeginDeliver(start, now)
	s.examined = d.qq.Examined()
	d.now = now
	d.last = Settled{Expired: append([]string(nil), st.Expired...),
		Failed: append([]string(nil), st.Failed...)}
	out := append([]string(nil), st.Delivered...)
	return out, nil
}

// Ack 确认指令。
func (s *Scheduler) Ack(dev, id string, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validTime(now) {
		return ErrInvalid
	}
	d, ok := s.devs[dev]
	if !ok {
		return ErrNoDevice
	}
	if now < d.now {
		return ErrClockBack
	}
	// Ack 不先做过期清除：指令必须待确认且未过期，过期指令直接报
	// ErrNoCmd，且本次拒绝不得引起任何副作用。
	if err := d.qq.Ack(id, now); err != nil {
		return mapErr(err)
	}
	d.now = now
	return nil
}

// Examined 返回最近一次 Deliver 考察的指令数（非导出计数器的测试钩子）。
func (s *Scheduler) Examined() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.examined
}

// Settled 是一次 Deliver 中离开队列的过期/失败指令清单。
type Settled struct {
	Expired []string
	Failed  []string
}

// LastSettled 返回最近一次 Deliver 的过期与失败清单（测试观察钩子）。
func (s *Scheduler) LastSettled(dev string) (Settled, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.devs[dev]
	if !ok {
		return Settled{}, false
	}
	return d.last, true
}

func mapErr(err error) error {
	switch err {
	case cmdq.ErrInvalid:
		return ErrInvalid
	case cmdq.ErrDupCmd:
		return ErrDupCmd
	case cmdq.ErrTooBig:
		return ErrTooBig
	case cmdq.ErrUnreachable:
		return ErrUnreachable
	case cmdq.ErrFull:
		return ErrFull
	case cmdq.ErrNoCmd:
		return ErrNoCmd
	default:
		return err
	}
}
