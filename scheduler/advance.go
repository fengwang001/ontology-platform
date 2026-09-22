package scheduler

import (
	"sort"

	"ontology/timer"
)

// Advance 推进 ticks 个 tick，触发所有到期定时器。
// ticks <= 0 或超过配置的 MaxAdvance 时返回错误且不改变任何状态。
func (s *Scheduler) Advance(ticks int64) error {
	if ticks <= 0 {
		return ErrInvalidAdvance
	}
	if ticks > s.maxAdv {
		return ErrAdvanceTooLarge
	}
	s.mu.Lock()
	s.touchedSlots, s.touchedTimers = 0, 0
	s.mu.Unlock()
	for i := int64(0); i < ticks; i++ {
		s.step()
	}
	return nil
}

// step 推进一个 tick：先推进游标并惰性降级，再收集到期者，最后按 seq 触发。
func (s *Scheduler) step() {
	s.mu.Lock()
	s.now++
	for lvl := 0; lvl < len(s.wheels); lvl++ {
		wrapped := s.wheels[lvl].Advance()
		s.touchedSlots++
		if lvl > 0 {
			s.cascadeLocked(lvl)
		}
		if !wrapped {
			break
		}
	}
	fire := s.collectLocked()
	s.mu.Unlock()
	s.fireAll(fire)
}

// cascadeLocked 高层游标落到新槽：取走整槽，按剩余 tick 重新定位到低层。
func (s *Scheduler) cascadeLocked(lvl int) {
	entries := s.wheels[lvl].Current().TakeAll()
	s.touchedTimers += int64(len(entries))
	for _, e := range entries {
		s.insertLocked(e.H.(*timer.Timer))
	}
}

// collectLocked 汇总本 tick 到期者：due 队列 + 层 0 当前槽，按 seq 升序。
func (s *Scheduler) collectLocked() []*timer.Timer {
	due := s.due
	s.due = nil
	entries := s.wheels[0].Current().TakeAll()
	s.touchedTimers += int64(len(due) + len(entries))
	out := make([]*timer.Timer, 0, len(due)+len(entries))
	out = append(out, due...)
	for _, e := range entries {
		out = append(out, e.H.(*timer.Timer))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq() < out[j].Seq() })
	return out
}

// fireAll 逐个触发：回调前复查状态，已取消的拦截；回调不持锁执行。
func (s *Scheduler) fireAll(list []*timer.Timer) {
	for _, t := range list {
		s.mu.Lock()
		if t.State() != timer.Pending {
			s.mu.Unlock()
			continue
		}
		t.Fire()
		delete(s.pending, t)
		s.mu.Unlock()
		if t.Fn != nil {
			t.Fn()
		}
	}
}
