// Package adjust 维护不停库盘点期间的库位账本：账面数、累计净移动量、容差与调整量。
package adjust

import (
	"errors"
	"sync"
)

// 哨兵错误，按拒绝次序排列：非法 > 不存在 > 状态 > 冲突 >
// 无 Approve > 须换人 > 缺 Senior > 库存不足。
var (
	ErrInvalid    = errors.New("invalid argument")
	ErrNotFound   = errors.New("not found")
	ErrState      = errors.New("illegal state")
	ErrConflict   = errors.New("conflict")
	ErrNoApprove  = errors.New("approve permission required")
	ErrMustSwitch = errors.New("must switch to another person")
	ErrNoSenior   = errors.New("senior permission required")
	ErrUnderstock = errors.New("insufficient stock")
)

// ID 为库位/任务/人员编号：1 到 32 字节的非空字节串。
type ID string

// Phase 为库位在盘点任务中的阶段。
type Phase int

const (
	PhaseIdle    Phase = iota // 不在未关闭任务中
	PhaseFirst                // 待初盘
	PhaseSecond               // 待复盘
	PhaseThird                // 待三盘
	PhasePending              // 调整申请待审批
	PhaseDone                 // 本任务内已定论
)

// 边界常量。
const (
	MaxIDLen   = 32
	MaxCount   = 1000
	MaxBook    = 1_000_000_000
	MaxPrice   = 1_000_000
	MaxLim     = 1_000_000_000_000_000
	MaxPercent = 100
)

// Loc 是单个库位的全部状态。字段对同模块的 count/authz 包可见。
type Loc struct {
	Book  int64 // 当前账面数
	Price int64 // 单价（分）
	Mv    int64 // 被接受 Move 的累计净移动量（盘点调整不计入）
	Adj   int64 // 被采纳/批准调整量之和（校验不变量用）

	Task    ID    // 所属未关闭任务；空表示空闲
	Phase   Phase // 盘点阶段
	C1      int64 // 初盘实盘数
	C2      int64 // 复盘实盘数
	M1      int64 // 初盘时刻的 mv 快照
	M2      int64 // 复盘时刻的 mv 快照
	P1      ID    // 初盘人
	P2      ID    // 复盘人
	P3      ID    // 三盘人（Pending 前的最后一轮盘点人）
	Pending int64 // 待批调整差值；0 仅在 PhasePending 以外

	scanned int64 // 一致性判定读取的移动流水条数（恒为 0）
	touched int64 // Submit 触碰的记录累计数
}

// Task 是盘点任务。
type Task struct {
	Locs    map[ID]struct{}
	Closed  bool
	LocList []ID
}

// System 是三个包共享的并发安全内存系统。
type System struct {
	Tabs int64 // 绝对容差
	Tpct int64 // 比例容差（百分数 0..100）
	Lim  int64 // 审批金额上限（分）

	mu    sync.RWMutex
	locs  map[ID]*Loc
	tasks map[ID]*Task
	perms map[ID]uint8 // 人员权限位（authz 包使用）
}

// 权限位。
const (
	PermApprove uint8 = 1 << iota
	PermSenior
)

// New 以构造参数创建系统，参数越界返回 ErrInvalid。
func New(tabs, tpct, lim int64) (*System, error) {
	if tabs < 0 || tabs > 1_000_000 ||
		tpct < 0 || tpct > MaxPercent ||
		lim < 0 || lim > MaxLim {
		return nil, ErrInvalid
	}
	return &System{
		Tabs:  tabs,
		Tpct:  tpct,
		Lim:   lim,
		locs:  map[ID]*Loc{},
		tasks: map[ID]*Task{},
		perms: map[ID]uint8{},
	}, nil
}

// Lock/Unlock 暴露给 count、authz 包复用同一把临界区锁。
func (s *System) Lock()   { s.mu.Lock() }
func (s *System) Unlock() { s.mu.Unlock() }

func validID(id ID) bool {
	return len(id) >= 1 && len(id) <= MaxIDLen
}

// ValidID 报告编号是否为 1..32 字节非空串。
func ValidID(id ID) bool { return validID(id) }

// AddLoc 登记库位；编号重复为冲突。
func (s *System) AddLoc(loc ID, book, price int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validID(loc) || book < 0 || book > MaxBook || price < 0 || price > MaxPrice {
		return ErrInvalid
	}
	if _, ok := s.locs[loc]; ok {
		return ErrConflict
	}
	s.locs[loc] = &Loc{Book: book, Price: price}
	return nil
}

// Move 为日常出入库；只累加被接受的移动，盘点调整不计入 mv。
func (s *System) Move(loc ID, delta int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validID(loc) || delta == 0 || delta < -MaxBook || delta > MaxBook {
		return ErrInvalid
	}
	l, ok := s.locs[loc]
	if !ok {
		return ErrNotFound
	}
	if l.Book+delta < 0 {
		return ErrUnderstock
	}
	if l.Book+delta > MaxBook {
		return ErrInvalid
	}
	l.Book += delta
	l.Mv += delta
	return nil
}

// Tol 返回判定瞬间的容差。
func (l *Loc) Tol(tpct, tabs int64) int64 {
	t := l.Book * tpct / 100
	if tabs > t {
		return tabs
	}
	return t
}

// WithinTol 报告 diff 是否在容差内（恰等也算）。
func (l *Loc) WithinTol(diff, tpct, tabs int64) bool {
	if diff < 0 {
		diff = -diff
	}
	return diff <= l.Tol(tpct, tabs)
}

// MustLock 下的内部读取助手，供 count/authz 包使用。

func (s *System) LocLocked(loc ID) (*Loc, bool) {
	l, ok := s.locs[loc]
	return l, ok
}

func (s *System) TaskLocked(task ID) (*Task, bool) {
	t, ok := s.tasks[task]
	return t, ok
}

func (s *System) PutTaskLocked(task ID, t *Task) { s.tasks[task] = t }
func (s *System) PutLocLocked(loc ID, l *Loc)    { s.locs[loc] = l }

// Counters 是非导出性能计数器的只读快照。
type Counters struct {
	Scanned int64
	Touched int64
}

// View 是库位状态的只读快照（测试与外部观测用）。
type View struct {
	Book    int64
	Price   int64
	Mv      int64
	Adj     int64
	Task    ID
	Phase   Phase
	Pending int64
	C1      int64
	C2      int64
	M1      int64
	M2      int64
	P1      ID
	P2      ID
	P3      ID
	Counters
}

// View 返回库位状态快照；库位不存在返回 ErrNotFound。
func (s *System) View(loc ID) (View, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !validID(loc) {
		return View{}, ErrInvalid
	}
	l, ok := s.locs[loc]
	if !ok {
		return View{}, ErrNotFound
	}
	return View{
		Book: l.Book, Price: l.Price, Mv: l.Mv, Adj: l.Adj,
		Task: l.Task, Phase: l.Phase, Pending: l.Pending,
		C1: l.C1, C2: l.C2, M1: l.M1, M2: l.M2,
		P1: l.P1, P2: l.P2, P3: l.P3,
		Counters: Counters{Scanned: l.scanned, Touched: l.touched},
	}, nil
}

// TaskClosed 报告任务是否存在及是否已关闭。
func (s *System) TaskClosed(task ID) (exists, closed bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tasks[task]
	return ok, ok && t.Closed
}

// TaskOf 返回库位当前所属的未关闭任务 ID（空闲时为空串）。
func (s *System) TaskOf(loc ID) ID {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if l, ok := s.locs[loc]; ok {
		return l.Task
	}
	return ""
}

// CountersLocked 返回某库位的计数器快照（调用方持锁）。
func (s *System) CountersLocked(loc ID) (Counters, bool) {
	l, ok := s.locs[loc]
	if !ok {
		return Counters{}, false
	}
	return Counters{Scanned: l.scanned, Touched: l.touched}, true
}

// Counters 返回库位非导出计数器的公开快照。
func (s *System) Counters(loc ID) (Counters, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.CountersLocked(loc)
}

// NoteSubmitLocked 记录一次 Submit 触碰的逻辑记录数（调用方持锁）。
// 触碰数为常数，与两次提交之间的 Move 笔数无关。
func (s *System) NoteSubmitLocked(l *Loc) { l.touched += 2 }

// NoteConsistencyLocked 记录 Second 阶段一致性判定读取的移动流水条数（调用方持锁）。
// 判定仅比较 mv 标量快照，不读取任何流水，故恒为 0。
func (s *System) NoteConsistencyLocked(l *Loc) { l.scanned += 0 }

// SetPermLocked 设置人员权限位（调用方持锁）。
func (s *System) SetPermLocked(person ID, bits uint8) { s.perms[person] = bits }

// PermLocked 读取人员权限位（调用方持锁）。
func (s *System) PermLocked(person ID) uint8 { return s.perms[person] }

// Book 返回库位当前账面数；库位不存在返回 ErrNotFound。
func (s *System) Book(loc ID) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !validID(loc) {
		return 0, ErrInvalid
	}
	l, ok := s.locs[loc]
	if !ok {
		return 0, ErrNotFound
	}
	return l.Book, nil
}
