// Package release 处理货物单元的装卸交接与放行判定（权限/复核/终态）。
package release

import (
	"sync"

	"ontology/budget"
	"ontology/probe"
)

// Perm 为操作员权限位。
type Perm uint8

const (
	// PermRelease 为放行权限位。
	PermRelease Perm = 1 << iota
	// PermQA 为质量复核权限位。
	PermQA
)

// Operator 携带权限位的操作员。
type Operator struct {
	Name string
	Perm Perm
}

// System 组合读数、预算与放行规则。
type System struct {
	mu sync.Mutex
	p  *probe.Store
	t  *budget.Tracker
	q  int64
}

// New 构造放行系统。q 为复核比例百分比（1..100）。
func New(p *probe.Store, t *budget.Tracker, q int64) *System {
	if q < 1 || q > 100 {
		panic(probe.ErrInvalid)
	}
	t.DisableClock()
	return &System{p: p, t: t, q: q}
}

// Reading 转发给 probe，时钟与并发由系统统一串行化。
func (s *System) Reading(device string, temp, now int64) error {
	if device == "" || now < 0 || now > 1_000_000_000 || temp < -1_000_000 || temp > 1_000_000 {
		return probe.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.p.Before(now) {
		return probe.ErrClock
	}
	if err := s.p.ReadingNoClock(device, temp, now); err != nil {
		return err
	}
	s.p.Advance(now)
	return nil
}

// Register 登记单元并装到设备上。
func (s *System) Register(unitID, device string, now int64) error {
	if unitID == "" || device == "" || now < 0 || now > 1_000_000_000 {
		return probe.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.p.Before(now) {
		return probe.ErrClock
	}
	if err := s.t.Register(unitID, device, now); err != nil {
		return err
	}
	s.p.Advance(now)
	return nil
}

// Unload 卸下单元。
func (s *System) Unload(unitID, device string, now int64) error {
	if unitID == "" || device == "" || now < 0 || now > 1_000_000_000 {
		return probe.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.p.Before(now) {
		return probe.ErrClock
	}
	if err := s.t.Unload(unitID, device, now); err != nil {
		return err
	}
	s.p.Advance(now)
	return nil
}

// Load 重装单元。
func (s *System) Load(unitID, device string, now int64) error {
	if unitID == "" || device == "" || now < 0 || now > 1_000_000_000 {
		return probe.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.p.Before(now) {
		return probe.ErrClock
	}
	if err := s.t.Load(unitID, device, now); err != nil {
		return err
	}
	s.p.Advance(now)
	return nil
}

// Evaluate 评估单元暴露。
func (s *System) Evaluate(unitID string, now int64) (budget.EvalResult, error) {
	if unitID == "" || now < 0 || now > 1_000_000_000 {
		return budget.EvalResult{}, probe.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.p.Before(now) {
		return budget.EvalResult{}, probe.ErrClock
	}
	r, err := s.t.Evaluate(unitID, now)
	if err != nil {
		return budget.EvalResult{}, err
	}
	s.p.Advance(now)
	return r, nil
}

// Release 由 operator 在 now 尝试放行单元。
func (s *System) Release(unitID string, op Operator, now int64) error {
	if unitID == "" || op.Name == "" || now < 0 || now > 1_000_000_000 {
		return probe.ErrInvalid
	}
	if s.p.Before(now) {
		return probe.ErrClock
	}
	if op.Perm&PermRelease == 0 {
		return probe.ErrForbidden
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.p.Before(now) {
		return probe.ErrClock
	}
	if !s.t.Exists(unitID) {
		return probe.ErrNotFound
	}
	if s.t.IsReleased(unitID) {
		return probe.ErrState // 已放行
	}
	// 只读预算，确定判废与复核需求（不写状态、不推进时钟）。
	e, err := s.t.PureE(unitID, now)
	if err != nil {
		return err
	}
	if s.t.SpoiledBy(unitID, now) {
		return probe.ErrSpoiled
	}
	if e*100 >= s.t.BMax()*s.q && op.Perm&PermQA == 0 {
		return probe.ErrQA
	}
	if _, ok := s.t.MarkReleased(unitID, now); !ok {
		return probe.ErrNotFound
	}
	s.p.Advance(now)
	return nil
}
