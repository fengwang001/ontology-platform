// Package sla 管理按工作日历计时、可暂停/恢复的 SLA 计时器。
package sla

import (
	"errors"
	"sync"

	"ontology/cal"
)

// 拒绝类别：参数非法与时钟错误复用 cal 中的同名哨兵。
var (
	ErrNotFound = errors.New("sla: timer not found")
	ErrExists   = errors.New("sla: timer already exists")
	ErrState    = errors.New("sla: invalid state for operation")
)

// segment 为一个运行段 [start, end)；end==-1 表示当前未闭合段。
type segment struct {
	start int64
	end   int64
}

// timer 保存预算与运行段。
type timer struct {
	budget int64
	segs   []segment
	paused bool
}

func (t *timer) openStart() int64 {
	return t.segs[len(t.segs)-1].start
}

// Manager 管理一组计时器，共享一个工作日历与其全局时钟。
type Manager struct {
	mu sync.Mutex
	c  *cal.Calendar
	m  map[string]*timer
}

// NewManager 创建计时器管理器。
func NewManager(c *cal.Calendar) *Manager {
	return &Manager{c: c, m: make(map[string]*timer)}
}

// Start 以预算 B 个工作分钟在 now 启动计时器（开始即运行态）。
func (m *Manager) Start(id []byte, budget, now int64) error {
	if len(id) == 0 || budget < 1 || budget > maxBudget {
		return cal.ErrArgument
	}
	if err := m.c.CheckClock(now); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := string(id)
	if _, ok := m.m[key]; ok {
		return ErrExists
	}
	m.m[key] = &timer{
		budget: budget,
		segs:   []segment{{start: now, end: -1}},
	}
	m.c.AdvanceClock(now)
	return nil
}

// Pause 在 now 暂停运行态计时器。
func (m *Manager) Pause(id []byte, now int64) error {
	if len(id) == 0 {
		return cal.ErrArgument
	}
	if err := m.c.CheckClock(now); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.m[string(id)]
	if !ok {
		return ErrNotFound
	}
	if t.paused {
		return ErrState
	}
	t.segs[len(t.segs)-1].end = now
	t.paused = true
	m.c.AdvanceClock(now)
	return nil
}

// Resume 在 now 恢复暂停态计时器。
func (m *Manager) Resume(id []byte, now int64) error {
	if len(id) == 0 {
		return cal.ErrArgument
	}
	if err := m.c.CheckClock(now); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.m[string(id)]
	if !ok {
		return ErrNotFound
	}
	if !t.paused {
		return ErrState
	}
	t.segs = append(t.segs, segment{start: now, end: -1})
	t.paused = false
	m.c.AdvanceClock(now)
	return nil
}

// Elapsed 返回到 now 为止累计的工作分钟数。
func (m *Manager) Elapsed(id []byte, now int64) (int64, error) {
	if len(id) == 0 {
		return 0, cal.ErrArgument
	}
	if err := m.c.CheckClock(now); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.m[string(id)]
	if !ok {
		return 0, ErrNotFound
	}
	v := m.elapsedAt(t, now)
	m.c.AdvanceClock(now)
	return v, nil
}

// Deadline 返回运行态计时器达到预算的时刻；暂停态返回 ok=false。
func (m *Manager) Deadline(id []byte, now int64) (int64, bool, error) {
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
	if t.paused {
		m.c.AdvanceClock(now)
		return 0, false, nil
	}
	tt, due := m.triggerLocked(t, t.budget)
	m.c.AdvanceClock(now)
	return tt, due, nil
}

// elapsedAt 计算到时刻 t（含当前未闭合段）的累计工作分钟。
func (m *Manager) elapsedAt(tm *timer, t int64) int64 {
	var total int64
	for i, seg := range tm.segs {
		end := seg.end
		if i == len(tm.segs)-1 && end == -1 {
			end = t
		}
		if end <= seg.start {
			continue
		}
		w, err := m.c.Work(seg.start, end)
		if err != nil {
			panic(err)
		}
		total += w
	}
	return total
}
