// Package release 维护货物单元的登记、装卸交接与放行。
package release

import "ontology/probe"

// Perm 是操作员权限位。
type Perm uint8

const (
	// PermRelease 允许执行放行。
	PermRelease Perm = 1 << iota
	// PermQA 允许在暴露达到复核比例时复核放行。
	PermQA
)

// Operator 是携带权限位的操作员。
type Operator struct {
	Name  string
	Perms Perm
}

// Interval 是一段装载区间 [Start, End)，End 为 -1 表示仍在装载。
type Interval struct {
	Device string
	Start  int64
	End    int64
}

// UnitView 是单元状态的只读快照。
type UnitView struct {
	RegisteredAt int64
	Intervals    []Interval
	Released     bool
	FrozenE      int64
}

// Evaluator 由 budget 包实现，供 Release 做判废与复核判定。
type Evaluator interface {
	// EvaluateLocked 在时钟锁内评估单元在 now 的 E 与是否判废；
	// 单元须存在且未放行。
	EvaluateLocked(unit string, now int64) (e int64, spoiled bool, err error)
	// NeedsQA 报告暴露 e 是否达到复核比例（恰等也算）。
	NeedsQA(e int64) bool
}

type unitState struct {
	registeredAt int64
	intervals    []Interval
	released     bool
	frozenE      int64
}

// Store 是单元登记与放行存储。
type Store struct {
	clock *probe.Clock
	probe *probe.Store
	units map[string]*unitState
	eval  Evaluator
}

// NewStore 构造单元存储。
func NewStore(clock *probe.Clock, probes *probe.Store) (*Store, error) {
	if clock == nil || probes == nil {
		return nil, probe.ErrInvalidParam
	}
	return &Store{clock: clock, probe: probes, units: make(map[string]*unitState)}, nil
}

// SetEvaluator 注入放行判定使用的评估器。
func (s *Store) SetEvaluator(ev Evaluator) { s.eval = ev }

// Unit 返回单元快照；调用方须持有时钟锁。
func (s *Store) Unit(name string) (UnitView, bool) {
	u := s.units[name]
	if u == nil {
		return UnitView{}, false
	}
	return UnitView{
		RegisteredAt: u.registeredAt,
		Intervals:    u.intervals,
		Released:     u.released,
		FrozenE:      u.frozenE,
	}, true
}

// Register 登记货物单元并装到设备上；设备须已有至少一条读数。
func (s *Store) Register(unit, device string, now int64) error {
	if unit == "" || device == "" || !probe.ValidNow(now) {
		return probe.ErrInvalidParam
	}
	s.clock.Lock()
	defer s.clock.Unlock()
	if err := s.clock.Check(now); err != nil {
		return err
	}
	if s.units[unit] != nil {
		return probe.ErrConflict
	}
	if !s.probe.HasReadings(device) {
		return probe.ErrNotFound
	}
	s.units[unit] = &unitState{
		registeredAt: now,
		intervals:    []Interval{{Device: device, Start: now, End: -1}},
	}
	s.clock.Advance(now)
	return nil
}

// Load 把单元再装上设备；单元须已登记、未放行且当前不在任何设备上。
func (s *Store) Load(unit, device string, now int64) error {
	if unit == "" || device == "" || !probe.ValidNow(now) {
		return probe.ErrInvalidParam
	}
	s.clock.Lock()
	defer s.clock.Unlock()
	if err := s.clock.Check(now); err != nil {
		return err
	}
	u := s.units[unit]
	if u == nil {
		return probe.ErrNotFound
	}
	if !s.probe.HasReadings(device) {
		return probe.ErrNotFound
	}
	if u.released {
		return probe.ErrState
	}
	if last := u.intervals[len(u.intervals)-1]; last.End == -1 {
		return probe.ErrState
	}
	u.intervals = append(u.intervals, Interval{Device: device, Start: now, End: -1})
	s.clock.Advance(now)
	return nil
}

// Unload 把单元从指定设备卸下；单元须正装载在该设备上。
func (s *Store) Unload(unit, device string, now int64) error {
	if unit == "" || device == "" || !probe.ValidNow(now) {
		return probe.ErrInvalidParam
	}
	s.clock.Lock()
	defer s.clock.Unlock()
	if err := s.clock.Check(now); err != nil {
		return err
	}
	u := s.units[unit]
	if u == nil {
		return probe.ErrNotFound
	}
	if !s.probe.HasReadings(device) {
		return probe.ErrNotFound
	}
	if u.released {
		return probe.ErrState
	}
	last := &u.intervals[len(u.intervals)-1]
	if last.End != -1 || last.Device != device {
		return probe.ErrState
	}
	last.End = now
	s.clock.Advance(now)
	return nil
}

// Release 放行单元：已判废拒绝；暴露达到复核比例时另需 QA 权限。
// 成功后单元进入终态，暴露冻结，仍在设备上的视为同时卸下。
func (s *Store) Release(unit string, op Operator, now int64) error {
	if unit == "" || op.Name == "" || !probe.ValidNow(now) {
		return probe.ErrInvalidParam
	}
	s.clock.Lock()
	defer s.clock.Unlock()
	if err := s.clock.Check(now); err != nil {
		return err
	}
	if op.Perms&PermRelease == 0 {
		return probe.ErrNoReleasePerm
	}
	u := s.units[unit]
	if u == nil {
		return probe.ErrNotFound
	}
	if u.released {
		return probe.ErrState
	}
	if s.eval == nil {
		return probe.ErrState
	}
	e, spoiled, err := s.eval.EvaluateLocked(unit, now)
	if err != nil {
		return err
	}
	if spoiled {
		return probe.ErrSpoiled
	}
	if s.eval.NeedsQA(e) && op.Perms&PermQA == 0 {
		return probe.ErrNoQAPerm
	}
	u.released = true
	u.frozenE = e
	if last := &u.intervals[len(u.intervals)-1]; last.End == -1 {
		last.End = now
	}
	s.clock.Advance(now)
	return nil
}
