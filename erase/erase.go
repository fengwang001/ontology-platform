// Package erase 实现数据主体擦除请求的账本核心：擦除单生命周期
// （Active/Deferred/Done）、逐系统确认与逾期检视。hold 与 restore 包
// 通过 Ledger.Tx / Ledger.View 在同一互斥锁下访问 Core，保证所有
// 并发操作等价于某个串行顺序。
package erase

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrParam     = errors.New("erase: invalid parameter")
	ErrPerm      = errors.New("erase: permission denied")
	ErrClock     = errors.New("erase: clock regression")
	ErrDuplicate = errors.New("erase: duplicate open request")
	ErrAlready   = errors.New("erase: subject already held")
	ErrNotHeld   = errors.New("erase: subject not held")
	ErrState     = errors.New("erase: invalid state")
	ErrNoBackup  = errors.New("erase: backup does not belong to system")
	ErrRestoring = errors.New("erase: system restoring")
)

// 角色。
const (
	RolePrivacy = 1 // 隐私官
	RoleLegal   = 2 // 法务
	RoleOps     = 3 // 系统运维
)

// 参数上界。
const (
	MaxS       = 8
	MaxT       = 1_000_000_000
	MaxSubject = 1_000_000
	MaxNow     = 1_000_000_000_000
)

// ValidNow 报告 now 是否在 [0, MaxNow] 内。
func ValidNow(now int64) bool { return now >= 0 && now <= MaxNow }

// CheckSubjectNow 校验主体与时刻的静态范围，越界返回 ErrParam。
func CheckSubjectNow(subject, now int64) error {
	if subject < 1 || subject > MaxSubject || !ValidNow(now) {
		return ErrParam
	}
	return nil
}

// Status 为擦除单状态。
type Status int

const (
	Active Status = iota
	Deferred
	Done
)

func (s Status) String() string {
	switch s {
	case Active:
		return "Active"
	case Deferred:
		return "Deferred"
	case Done:
		return "Done"
	}
	return "?"
}

// SysStatus 为下游系统状态。
type SysStatus int

const (
	Ready SysStatus = iota
	Restoring
)

func (s SysStatus) String() string {
	if s == Restoring {
		return "Restoring"
	}
	return "Ready"
}

// Erasure 为一张擦除单。AckAt[s-1] 为系统 s 的确认时刻，-1 表示待确认。
type Erasure struct {
	ID          int
	Subject     int64
	Status      Status
	RequestedAt int64
	Deadline    int64 // 仅 Active 时有效
	AckAt       []int64
}

// Pending 返回仍待确认的系统编号升序列表。
func (e *Erasure) Pending() []int {
	var p []int
	for i, a := range e.AckAt {
		if a < 0 {
			p = append(p, i+1)
		}
	}
	return p
}

// Backup 为一个备份点，编号全局从 1 递增。
type Backup struct {
	ID     int
	System int
	Tb     int64
}

// OverdueEntry 为一条逾期记录：擦除单编号与仍待确认的系统。
type OverdueEntry struct {
	ID      int
	Pending []int
}

type system struct {
	status SysStatus
	todo   map[int]bool // 恢复重放待办，非空当且仅当 Restoring
}

type activeEnt struct {
	deadline int64
	id       int
}

// Core 为账本全部可变状态，只能在 Ledger 的锁内（Tx/View）访问。
type Core struct {
	sysCount int
	limit    int64
	maxNow   int64

	erasures  []*Erasure
	openBySub map[int64]*Erasure // 仅 Active/Deferred
	held      map[int64]bool
	backups   []Backup
	systems   []system
	active    []activeEnt // 按 (deadline, id) 升序

	inspected int64 // 最近一次 Overdue 检视的擦除单数（非导出计数器）
}

// Ledger 为串行化入口：所有操作在同一把锁下执行。
type Ledger struct {
	mu sync.Mutex
	c  *Core
}

// New 构造账本：S 为下游系统数（1..8），T 为擦除时限（1..1e9）。
func New(sysCount int, limit int64) (*Ledger, error) {
	if sysCount < 1 || sysCount > MaxS || limit < 1 || limit > MaxT {
		return nil, ErrParam
	}
	return &Ledger{c: &Core{
		sysCount:  sysCount,
		limit:     limit,
		openBySub: make(map[int64]*Erasure),
		held:      make(map[int64]bool),
		systems:   make([]system, sysCount),
	}}, nil
}

// Tx 在锁内按固定拒绝次序执行操作：动态参数校验 → 权限 → 时钟回退 →
// 状态类。仅当 op 成功时推进最大 now；任何拒绝都不改变状态。
func (l *Ledger) Tx(now int64, role, wantRole int, paramCheck, op func(*Core) error) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if paramCheck != nil {
		if err := paramCheck(l.c); err != nil {
			return err
		}
	}
	if role != wantRole {
		return ErrPerm
	}
	if now < l.c.maxNow {
		return ErrClock
	}
	if err := op(l.c); err != nil {
		return err
	}
	l.c.maxNow = now
	return nil
}

// View 在锁内执行只读检视，不做时钟检查也不推进最大 now。
func (l *Ledger) View(fn func(*Core) error) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return fn(l.c)
}

// SysCount 返回下游系统数（构造后不可变，读取无需锁）。
func (l *Ledger) SysCount() int { return l.c.sysCount }

// OverdueInspected 返回最近一次 Overdue 检视的擦除单数（诊断/测试用）。
func (l *Ledger) OverdueInspected() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.c.inspected
}

// ---- 只读访问器（须在 View/Tx 内使用） ----

func (c *Core) NumErasures() int { return len(c.erasures) }
func (c *Core) NumBackups() int  { return len(c.backups) }
func (c *Core) MaxNow() int64    { return c.maxNow }
func (c *Core) SysCount() int    { return c.sysCount }
func (c *Core) Limit() int64     { return c.limit }

// Erasure 返回编号为 e（1 起）的擦除单，越界返回 nil。
func (c *Core) Erasure(e int) *Erasure {
	if e < 1 || e > len(c.erasures) {
		return nil
	}
	return c.erasures[e-1]
}

// GetBackup 返回编号为 b（1 起）的备份点。
func (c *Core) GetBackup(b int) (Backup, bool) {
	if b < 1 || b > len(c.backups) {
		return Backup{}, false
	}
	return c.backups[b-1], true
}

// IsHeld 报告主体当前是否被法律保留。
func (c *Core) IsHeld(subject int64) bool { return c.held[subject] }

// SystemStatus 返回系统 s 的状态。
func (c *Core) SystemStatus(s int) SysStatus { return c.systems[s-1].status }

// Todo 返回系统 s 的恢复重放待办（升序副本）。
func (c *Core) Todo(s int) []int {
	var out []int
	for id := range c.systems[s-1].todo {
		out = append(out, id)
	}
	sort.Ints(out)
	return out
}

// ---- 状态机操作（须在 Tx 内调用） ----

// RequestErasure 新建擦除单；主体已有 Active/Deferred 单时 ErrDuplicate。
func (c *Core) RequestErasure(subject int64, now int64) (int, error) {
	if _, ok := c.openBySub[subject]; ok {
		return 0, ErrDuplicate
	}
	e := &Erasure{
		ID:          len(c.erasures) + 1,
		Subject:     subject,
		RequestedAt: now,
		AckAt:       make([]int64, c.sysCount),
	}
	for i := range e.AckAt {
		e.AckAt[i] = -1
	}
	if c.held[subject] {
		e.Status = Deferred
	} else {
		e.Status = Active
		e.Deadline = now + c.limit
		c.insertActive(e.ID, e.Deadline)
	}
	c.erasures = append(c.erasures, e)
	c.openBySub[subject] = e
	return e.ID, nil
}

// AckErasure 记录系统 s 对擦除单 e 的确认；全部确认后 e 变 Done。
func (c *Core) AckErasure(e, s int, now int64) error {
	er := c.erasures[e-1]
	if er.Status != Active || er.AckAt[s-1] >= 0 {
		return ErrState
	}
	er.AckAt[s-1] = now
	for _, a := range er.AckAt {
		if a < 0 {
			return nil
		}
	}
	er.Status = Done
	c.removeActive(er.ID, er.Deadline)
	delete(c.openBySub, er.Subject)
	return nil
}

// HoldSubject 置法律保留；已保留则 ErrAlready。不影响已 Active 的擦除单。
func (c *Core) HoldSubject(subject int64) error {
	if c.held[subject] {
		return ErrAlready
	}
	c.held[subject] = true
	return nil
}

// ReleaseSubject 解除保留；未保留则 ErrNotHeld。该主体的 Deferred 单
// 变 Active，时限自解除起算。
func (c *Core) ReleaseSubject(subject int64, now int64) error {
	if !c.held[subject] {
		return ErrNotHeld
	}
	delete(c.held, subject)
	if e, ok := c.openBySub[subject]; ok && e.Status == Deferred {
		e.Status = Active
		e.Deadline = now + c.limit
		c.insertActive(e.ID, e.Deadline)
	}
	return nil
}

// AddBackup 为系统 s 新建备份点，返回全局递增编号。
func (c *Core) AddBackup(s int, now int64) int {
	b := Backup{ID: len(c.backups) + 1, System: s, Tb: now}
	c.backups = append(c.backups, b)
	return b.ID
}

// RestoreBackup 计算重放集 L（ack(e,s) 存在且 > tb，按编号升序）；
// L 非空则系统进入 Restoring 并记待办，为空则保持 Ready。
func (c *Core) RestoreBackup(s, b int) ([]int, error) {
	bk := c.backups[b-1]
	if bk.System != s {
		return nil, ErrNoBackup
	}
	if c.systems[s-1].status != Ready {
		return nil, ErrState
	}
	var L []int
	for _, e := range c.erasures {
		if a := e.AckAt[s-1]; a >= 0 && a > bk.Tb {
			L = append(L, e.ID)
		}
	}
	if len(L) > 0 {
		todo := make(map[int]bool, len(L))
		for _, id := range L {
			todo[id] = true
		}
		c.systems[s-1] = system{status: Restoring, todo: todo}
	}
	return L, nil
}

// CompleteReapply 从系统 s 的待办中移除 e；待办清空后回到 Ready。
func (c *Core) CompleteReapply(s, e int) error {
	sys := &c.systems[s-1]
	if sys.status != Restoring || !sys.todo[e] {
		return ErrState
	}
	delete(sys.todo, e)
	if len(sys.todo) == 0 {
		sys.status = Ready
		sys.todo = nil
	}
	return nil
}

// OverdueEntries 返回 Active 且 deadline <= now 的擦除单（编号升序）
// 及各自仍待确认的系统。只读，不做时钟检查，不推进最大 now。
func (c *Core) OverdueEntries(now int64) []OverdueEntry {
	c.inspected = 0
	var res []OverdueEntry
	for _, a := range c.active {
		c.inspected++
		if a.deadline > now {
			break
		}
		res = append(res, OverdueEntry{ID: a.id, Pending: c.erasures[a.id-1].Pending()})
	}
	sort.Slice(res, func(i, j int) bool { return res[i].ID < res[j].ID })
	return res
}

func (c *Core) insertActive(id int, deadline int64) {
	i := sort.Search(len(c.active), func(i int) bool {
		a := c.active[i]
		return a.deadline > deadline || (a.deadline == deadline && a.id >= id)
	})
	c.active = append(c.active, activeEnt{})
	copy(c.active[i+1:], c.active[i:])
	c.active[i] = activeEnt{deadline: deadline, id: id}
}

func (c *Core) removeActive(id int, deadline int64) {
	i := sort.Search(len(c.active), func(i int) bool {
		a := c.active[i]
		return a.deadline > deadline || (a.deadline == deadline && a.id >= id)
	})
	if i < len(c.active) && c.active[i] == (activeEnt{deadline: deadline, id: id}) {
		c.active = append(c.active[:i], c.active[i+1:]...)
	}
}

// ---- Ledger 层操作 ----

// Request 新建擦除单（角色：隐私官），返回擦除单编号。
func (l *Ledger) Request(role int, subject int64, now int64) (int, error) {
	if err := CheckSubjectNow(subject, now); err != nil {
		return 0, err
	}
	var id int
	err := l.Tx(now, role, RolePrivacy, nil, func(c *Core) error {
		var err error
		id, err = c.RequestErasure(subject, now)
		return err
	})
	if err != nil {
		return 0, err
	}
	return id, nil
}

// Ack 记录逐系统确认（角色：系统运维）。
func (l *Ledger) Ack(role, e, s int, now int64) error {
	if s < 1 || s > l.c.sysCount || !ValidNow(now) {
		return ErrParam
	}
	return l.Tx(now, role, RoleOps,
		func(c *Core) error {
			if e < 1 || e > c.NumErasures() {
				return ErrParam
			}
			return nil
		},
		func(c *Core) error { return c.AckErasure(e, s, now) })
}

// Overdue 返回 Active 且 deadline 不大于 now 的擦除单及待确认系统。
// 只读：不做时钟检查，也不推进最大 now。
func (l *Ledger) Overdue(now int64) []OverdueEntry {
	var res []OverdueEntry
	_ = l.View(func(c *Core) error {
		res = c.OverdueEntries(now)
		return nil
	})
	return res
}
