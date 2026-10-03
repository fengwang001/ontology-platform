package sla

import "ontology/cal"

// maxBudget 为 B 的上界（10^9 工作分钟）。
const maxBudget = int64(1_000_000_000)

// Threshold 返回阈值档 q（50/80/100）对应的工作分钟阈值。
// 调用方需保证 q 合法。
func Threshold(budget int64, q uint8) int64 {
	return (budget*int64(q) + 99) / 100
}

// Trigger 返回达到 target 个工作分钟的最小时刻。
// 暂停且尚未达到 target 时返回 ok=false；结果超出时间轴同样返回 ok=false。
func (m *Manager) Trigger(id []byte, target, now int64) (int64, bool, error) {
	if len(id) == 0 {
		return 0, false, cal.ErrArgument
	}
	if err := m.c.CheckClock(now); err != nil {
		return 0, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.m[string(id)]
	if !ok {
		return 0, false, ErrNotFound
	}
	tt, due := m.triggerLocked(t, target)
	m.c.AdvanceClock(now)
	return tt, due, nil
}

// IDs 返回所有计时器 id 的拷贝（供 alert 遍历）。
func (m *Manager) IDs() [][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][]byte, 0, len(m.m))
	for k := range m.m {
		out = append(out, []byte(k))
	}
	return out
}

// Budget 返回计时器预算。
func (m *Manager) Budget(id []byte) (int64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.m[string(id)]
	if !ok {
		return 0, false
	}
	return t.budget, true
}

// triggerLocked 在已持有 m.mu 时计算达到 target 的最小时刻。
func (m *Manager) triggerLocked(tm *timer, target int64) (int64, bool) {
	var closed int64
	for i, seg := range tm.segs {
		if seg.end == -1 && i == len(tm.segs)-1 {
			remain := target - closed
			if remain <= 0 {
				if x := tm.segs[i].start; x <= cal.MaxTime {
					return x, true
				}
				return 0, false
			}
			return m.c.Advance(seg.start, remain)
		}
		w, err := m.c.Work(seg.start, seg.end)
		if err != nil {
			panic(err)
		}
		if closed+w >= target {
			return m.c.Advance(seg.start, target-closed)
		}
		closed += w
	}
	return 0, false
}
