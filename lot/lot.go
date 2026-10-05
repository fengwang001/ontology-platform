// Package lot 管理检验流与检验批：提交、判定、复检、暂停与恢复。
//
// 检验流以（供应商，物料）为键，首次提交时自动建立，初始严格度 Normal。
// 每个流同一时刻至多一个未判定批（含复检轮）；批号全局唯一。
// 所有操作由单互斥锁串行化，并发调用等价于某个串行顺序，不同流互不影响。
package lot

import (
	"errors"
	"sync"

	"ontology/plan"
	"ontology/switchrule"
)

// 拒绝原因的哨兵错误，可用 errors.Is 区分。
// 同一操作违反多条规则时只报第一个，优先级为：
// ErrInvalidParam > ErrPermission > ErrNotFound > ErrSuspended >
// ErrState > ErrConflict > ErrOutOfRange。
var (
	ErrInvalidParam = errors.New("lot: 参数非法")
	ErrPermission   = errors.New("lot: 无权限")
	ErrNotFound     = errors.New("lot: 不存在")
	ErrSuspended    = errors.New("lot: 流已暂停")
	ErrState        = errors.New("lot: 状态不符")
	ErrConflict     = errors.New("lot: 冲突")
	ErrOutOfRange   = errors.New("lot: 数量越界")
)

// Status 为检验批的判定状态。
type Status int

const (
	// Pending 待判定（含复检轮）。
	Pending Status = iota
	// Released 已放行（初检或复检接收）。
	Released
	// Rejected 初检拒收，可复检。
	Rejected
	// Scrapped 复检拒收，终态。
	Scrapped
)

func (s Status) String() string {
	switch s {
	case Pending:
		return "Pending"
	case Released:
		return "Released"
	case Rejected:
		return "Rejected"
	case Scrapped:
		return "Scrapped"
	}
	return "Unknown"
}

// Role 为操作员权限角色。
type Role int

const (
	// RoleInspector 检验员，可 Record。
	RoleInspector Role = iota
	// RoleManager 经理，可 Resume。
	RoleManager
)

// Operator 为一次操作的操作员及其角色。
type Operator struct {
	ID    string
	Roles []Role
}

// Has 报告操作员是否持有角色 r。
func (o Operator) Has(r Role) bool {
	for _, x := range o.Roles {
		if x == r {
			return true
		}
	}
	return false
}

// StreamKey 为检验流的键：（供应商，物料）。
type StreamKey struct {
	Supplier string
	Material string
}

// Lot 为检验批。方案在提交时刻按该流严格度快照在批上，复检轮重新快照。
type Lot struct {
	ID       string
	Stream   StreamKey
	N        int           // 批量
	Severity plan.Severity // 提交（或复检开启）时刻的严格度
	Plan     plan.Plan     // 方案快照，n 已按不超过 N 截断
	Status   Status
	D        int  // 样本中的不合格数，判定后有效
	Decided  bool // 是否已判定
	Retest   bool // 是否处于（或经历过）复检轮
}

// maxN 为批量上限。
const maxN = 1_000_000

// stream 为一个检验流的运行时状态。
type stream struct {
	rule    *switchrule.State
	pending *Lot // 当前未判定批（含复检轮），至多一个
}

// Manager 为检验流与检验批的注册表，并发安全。
type Manager struct {
	mu      sync.Mutex
	table   *plan.Table
	streams map[StreamKey]*stream
	lots    map[string]*Lot
}

// NewManager 以方案表 t 创建放行器。
func NewManager(t *plan.Table) *Manager {
	return &Manager{
		table:   t,
		streams: make(map[StreamKey]*stream),
		lots:    make(map[string]*Lot),
	}
}

// Submit 向流提交一个批量为 n、批号为 id 的检验批。
// 流不存在时自动建立（初始 Normal）；方案按当前严格度快照，
// n 大于 N 时取 n=N 而 Ac、Re 不变。
func (m *Manager) Submit(key StreamKey, id string, n int) (Lot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if key.Supplier == "" || key.Material == "" || id == "" || n < 1 || n > maxN {
		return Lot{}, ErrInvalidParam
	}
	if _, ok := m.table.Lookup(n); !ok {
		return Lot{}, ErrInvalidParam
	}
	st := m.streams[key]
	if st != nil && st.rule.Severity() == plan.Suspended {
		return Lot{}, ErrSuspended
	}
	if st != nil && st.pending != nil {
		return Lot{}, ErrState
	}
	if _, dup := m.lots[id]; dup {
		return Lot{}, ErrConflict
	}
	if st == nil {
		st = &stream{rule: switchrule.New(m.table.Lr())}
		m.streams[key] = st
	}
	p, _ := m.table.Plan(n, st.rule.Severity())
	if p.N > n {
		p.N = n
	}
	l := &Lot{
		ID:       id,
		Stream:   key,
		N:        n,
		Severity: st.rule.Severity(),
		Plan:     p,
		Status:   Pending,
	}
	m.lots[id] = l
	st.pending = l
	return *l, nil
}

// Record 录入批 id 样本中的不合格数 d 并判定，需 Inspector 权限。
// d<=Ac 接收，d>=Re 拒收，Ac<d<Re 为边缘接收（本批接收但触发转移）。
// 初检批判定后按当前严格度执行转移；复检批不进入任何窗口与计数。
func (m *Manager) Record(id string, d int, op Operator) (Lot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id == "" || d < 0 {
		return Lot{}, ErrInvalidParam
	}
	if !op.Has(RoleInspector) {
		return Lot{}, ErrPermission
	}
	l, ok := m.lots[id]
	if !ok {
		return Lot{}, ErrNotFound
	}
	if l.Status != Pending {
		return Lot{}, ErrState
	}
	if d > l.Plan.N {
		return Lot{}, ErrOutOfRange
	}
	st := m.streams[l.Stream]
	st.pending = nil
	l.D = d
	l.Decided = true
	var v switchrule.Verdict
	switch {
	case d <= l.Plan.Ac:
		v = switchrule.Accept
	case d >= l.Plan.Re:
		v = switchrule.Reject
	default:
		v = switchrule.Marginal
	}
	if l.Retest {
		if v == switchrule.Reject {
			l.Status = Scrapped
		} else {
			l.Status = Released
		}
		return *l, nil
	}
	if v == switchrule.Reject {
		l.Status = Rejected
	} else {
		l.Status = Released
	}
	st.rule.Record(v, d)
	return *l, nil
}

// Resubmit 对 Rejected 且未复检过的批开启复检轮：方案固定取 Tightened 档
// （按原 N，n 同样不超过 N），判定结果不进入任何窗口与计数，不触发转移。
// 流处于 Suspended 时也可复检。
func (m *Manager) Resubmit(id string) (Lot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id == "" {
		return Lot{}, ErrInvalidParam
	}
	l, ok := m.lots[id]
	if !ok {
		return Lot{}, ErrNotFound
	}
	if l.Status != Rejected {
		return Lot{}, ErrState
	}
	st := m.streams[l.Stream]
	if st.pending != nil {
		return Lot{}, ErrState
	}
	p, _ := m.table.Plan(l.N, plan.Tightened)
	if p.N > l.N {
		p.N = l.N
	}
	l.Plan = p
	l.Severity = plan.Tightened
	l.Status = Pending
	l.D = 0
	l.Decided = false
	l.Retest = true
	st.pending = l
	return *l, nil
}

// Resume 恢复已暂停的检验流为 Tightened 并清空计数，需 Manager 权限。
func (m *Manager) Resume(key StreamKey, op Operator) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if key.Supplier == "" || key.Material == "" {
		return ErrInvalidParam
	}
	if !op.Has(RoleManager) {
		return ErrPermission
	}
	st, ok := m.streams[key]
	if !ok {
		return ErrNotFound
	}
	if st.rule.Severity() != plan.Suspended {
		return ErrState
	}
	st.rule.Resume()
	return nil
}

// Lookup 按批号查询检验批快照。
func (m *Manager) Lookup(id string) (Lot, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.lots[id]
	if !ok {
		return Lot{}, false
	}
	return *l, true
}

// SeverityOf 查询流的当前严格度；流不存在时 ok 为 false。
func (m *Manager) SeverityOf(key StreamKey) (plan.Severity, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.streams[key]
	if !ok {
		return plan.Normal, false
	}
	return st.rule.Severity(), true
}
