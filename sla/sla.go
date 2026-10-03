package sla

import (
	"sync"

	"ontology/cal"
)

const MaxBudget = int64(1_000_000_000)

type seg struct{ start, end int64 }

type timer struct {
	budget int64
	segs   []seg // 已结束段 end>0；当前运行段为末段 end==0
	paused bool
}

// Manager 管理计时器运行段；全局时钟复用 cal.Calendar 的时钟。
type Manager struct {
	mu sync.Mutex
	c  *cal.Calendar
	t  map[string]*timer
}

func NewManager(c *cal.Calendar) *Manager {
	return &Manager{c: c, t: map[string]*timer{}}
}

func validID(id []byte) bool { return len(id) > 0 }

func validNow(now int64) bool { return now >= 0 && now <= cal.MaxTime }

func mapCalErr(err error) error {
	switch err {
	case cal.ErrInvalid:
		return ErrInvalid
	case cal.ErrClock:
		return cal.ErrClock
	default:
		return err
	}
}

func (m *Manager) Start(id []byte, budget, now int64) error {
	if !validID(id) || budget < 1 || budget > MaxBudget || !validNow(now) {
		return ErrInvalid
	}
	if err := m.c.CheckClock(now); err != nil {
		return mapCalErr(err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := string(id)
	if _, ok := m.t[key]; ok {
		return ErrExists
	}
	m.t[key] = &timer{budget: budget, segs: []seg{{start: now}}}
	m.c.AdvanceClock(now)
	return nil
}

func (m *Manager) Pause(id []byte, now int64) error {
	if !validID(id) || !validNow(now) {
		return ErrInvalid
	}
	if err := m.c.CheckClock(now); err != nil {
		return mapCalErr(err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tm, ok := m.t[string(id)]
	if !ok {
		return ErrNotFound
	}
	if tm.paused {
		return ErrState
	}
	tm.segs[len(tm.segs)-1].end = now
	tm.paused = true
	m.c.AdvanceClock(now)
	return nil
}

func (m *Manager) Resume(id []byte, now int64) error {
	if !validID(id) || !validNow(now) {
		return ErrInvalid
	}
	if err := m.c.CheckClock(now); err != nil {
		return mapCalErr(err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tm, ok := m.t[string(id)]
	if !ok {
		return ErrNotFound
	}
	if !tm.paused {
		return ErrState
	}
	tm.segs = append(tm.segs, seg{start: now})
	tm.paused = false
	m.c.AdvanceClock(now)
	return nil
}

func (m *Manager) Elapsed(id []byte, now int64) (int64, error) {
	if !validID(id) || !validNow(now) {
		return 0, ErrInvalid
	}
	if err := m.c.CheckClock(now); err != nil {
		return 0, mapCalErr(err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tm, ok := m.t[string(id)]
	if !ok {
		return 0, ErrNotFound
	}
	m.c.RLock()
	v := m.elapsedLocked(tm, now)
	m.c.RUnlock()
	m.c.AdvanceClock(now)
	return v, nil
}

func (m *Manager) Deadline(id []byte, now int64) (int64, bool, error) {
	if !validID(id) || !validNow(now) {
		return 0, false, ErrInvalid
	}
	if err := m.c.CheckClock(now); err != nil {
		return 0, false, mapCalErr(err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tm, ok := m.t[string(id)]
	if !ok {
		return 0, false, ErrNotFound
	}
	m.c.RLock()
	var v int64
	var vok bool
	if !tm.paused {
		v, vok = m.triggerLocked(tm, tm.budget)
	}
	m.c.RUnlock()
	m.c.AdvanceClock(now)
	return v, vok, nil
}
