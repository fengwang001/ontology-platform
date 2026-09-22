// Package scheduler 对外调度器：Add/Cancel/Reset/Advance，
// 管理各层轮与注入时钟。时间完全由 Advance 推进。
package scheduler

import (
	"fmt"
	"sync"

	"ontology/cascade"
	"ontology/timer"
	"ontology/wheel"
)

// Scheduler 分层时间轮调度器，所有公开方法可并发调用。
type Scheduler struct {
	mu       sync.Mutex
	now      int64
	seq      uint64
	layout   cascade.Layout
	wheels   []*wheel.Wheel
	pending  map[*timer.Timer]struct{}
	due      []*timer.Timer // deadline <= now，下一次 tick 触发
	maxAdv   int64
	maxTm    int
	maxDelay int64

	touchedSlots  int64 // 最近一次 Advance 触碰的槽数
	touchedTimers int64 // 最近一次 Advance 触碰的定时器数
}

// Add 注册定时器：d 个 tick 后触发 fn。d 为 0 时在下一次 Advance 触发。
func (s *Scheduler) Add(d int64, fn func()) (*timer.Timer, error) {
	if d < 0 {
		return nil, ErrNegativeDelay
	}
	if d > s.maxDelay {
		return nil, ErrDelayTooLarge
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) >= s.maxTm {
		return nil, ErrTooManyTimers
	}
	t := timer.New(s.nextSeqLocked(), s.now+d, fn)
	s.insertLocked(t)
	s.pending[t] = struct{}{}
	return t, nil
}

// Cancel 取消定时器。重复取消返回 ErrAlreadyCancelled，
// 已触发返回 ErrAlreadyFired，未知句柄返回 ErrUnknownTimer。
func (s *Scheduler) Cancel(t *timer.Timer) error {
	if t == nil {
		return ErrUnknownTimer
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.stateErrLocked(t); err != nil {
		return err
	}
	t.Cancel()
	s.removeLocked(t)
	return nil
}

// Reset 重设定时器：从当前时刻起算 d 个 tick 后触发（视为重新注册）。
func (s *Scheduler) Reset(t *timer.Timer, d int64) error {
	if d < 0 {
		return ErrNegativeDelay
	}
	if d > s.maxDelay {
		return ErrDelayTooLarge
	}
	if t == nil {
		return ErrUnknownTimer
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.stateErrLocked(t); err != nil {
		return err
	}
	s.wheels[t.Level()].Slot(s.layout.Slot(t.Deadline(), t.Level())).Remove(t)
	t.Reset(s.nextSeqLocked(), s.now+d)
	s.insertLocked(t)
	return nil
}

// Pending 返回待触发定时器数量。
func (s *Scheduler) Pending() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending)
}

// Check 自检：轮中元素总数与调度器记录一致；槽内无已取消/已触发元素；
// 各层游标与累计推进量的换算自洽。全部通过返回 nil。
func (s *Scheduler) Check() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	total := len(s.due)
	for lvl, w := range s.wheels {
		if want := s.layout.Cursor(s.now, lvl); w.Pos() != want {
			return fmt.Errorf("wheel %d cursor %d, want %d", lvl, w.Pos(), want)
		}
		for i := 0; i < w.Size(); i++ {
			for _, e := range w.Slot(i).Entries() {
				t := e.H.(*timer.Timer)
				if t.State() != timer.Pending {
					return fmt.Errorf("wheel %d slot %d holds %s timer", lvl, i, t.State())
				}
				total++
			}
		}
	}
	if total != len(s.pending) {
		return fmt.Errorf("wheels hold %d timers, scheduler tracks %d", total, len(s.pending))
	}
	return nil
}

// stateErrLocked 把句柄状态映射为可判定的幂等错误。
func (s *Scheduler) stateErrLocked(t *timer.Timer) error {
	switch t.State() {
	case timer.Cancelled:
		return ErrAlreadyCancelled
	case timer.Fired:
		return ErrAlreadyFired
	}
	if _, ok := s.pending[t]; !ok {
		return ErrUnknownTimer
	}
	return nil
}

// removeLocked 从登记处与所在槽移除句柄（due 队列中的由触发前状态检查拦截）。
func (s *Scheduler) removeLocked(t *timer.Timer) {
	delete(s.pending, t)
	s.wheels[t.Level()].Slot(s.layout.Slot(t.Deadline(), t.Level())).Remove(t)
}

// insertLocked 按剩余 tick 定位层与槽；deadline <= now 的进 due 队列。
func (s *Scheduler) insertLocked(t *timer.Timer) {
	r := t.Deadline() - s.now
	if r <= 0 {
		s.due = append(s.due, t)
		return
	}
	lvl := s.layout.Level(r)
	t.SetLevel(lvl)
	s.wheels[lvl].Slot(s.layout.Slot(t.Deadline(), lvl)).Insert(t.Entry())
	s.touchedSlots++
}

func (s *Scheduler) nextSeqLocked() uint64 {
	s.seq++
	return s.seq
}
