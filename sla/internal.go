package sla

import "ontology/cal"

// Lock/Unlock 供 alert 包按 alert→sla→cal 的固定顺序持锁复合查询。
func (m *Manager) Lock()   { m.mu.Lock() }
func (m *Manager) Unlock() { m.mu.Unlock() }

// Cal 返回底层日历。
func (m *Manager) Cal() *cal.Calendar { return m.c }

// TimerLocked 按 id 取计时器（调用方持 m 锁）。
func (m *Manager) TimerLocked(id string) *TimerInfo {
	tm := m.t[id]
	if tm == nil {
		return nil
	}
	return &TimerInfo{tm: tm}

}

// IDsLocked 返回全部计时器 id（调用方持 m 锁）。
func (m *Manager) IDsLocked() []string {
	ids := make([]string, 0, len(m.t))
	for k := range m.t {
		ids = append(ids, k)
	}
	return ids
}

// TimerInfo 是 alert 包使用的只读视图（底层指针，不复制）。
type TimerInfo struct{ tm *timer }

func (ti *TimerInfo) Paused() bool { return ti.tm.paused }

func (ti *TimerInfo) Budget() int64 { return ti.tm.budget }

// ElapsedLocked 返回 now 时刻已用工作分钟（调用方持 m 与 cal 读锁）。
func (m *Manager) ElapsedLocked(ti *TimerInfo, now int64) int64 {
	return m.elapsedLocked(ti.tm, now)
}

// TriggerLocked 返回累计达到 need 个工作分钟的时刻；>10^12 时 ok=false。
func (m *Manager) TriggerLocked(ti *TimerInfo, need int64) (int64, bool) {
	return m.triggerLocked(ti.tm, need)
}

func (m *Manager) elapsedLocked(tm *timer, now int64) int64 {
	var total int64
	for i, s := range tm.segs {
		end := s.end
		if i == len(tm.segs)-1 && !tm.paused {
			end = now
		}
		if end > s.start {
			total += m.c.WorkLocked(s.start, end)
		}
	}
	return total
}

func (m *Manager) triggerLocked(tm *timer, need int64) (int64, bool) {
	for _, s := range tm.segs {
		if s.end > 0 {
			w := m.c.WorkLocked(s.start, s.end)
			if w >= need {
				return m.c.AdvanceLocked(s.start, need)
			}
			need -= w
		} else {
			if tm.paused {
				return 0, false
			}
			return m.c.AdvanceLocked(s.start, need)
		}
	}
	return 0, false
}
