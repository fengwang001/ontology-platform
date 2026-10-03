// Package saga 管理 Saga 实例：先写日志、后登记副作用，
// 崩溃后只凭日志前缀推导恢复计划（见 DESIGN.md）。
package saga

import (
	"errors"
	"sync"
	"sync/atomic"

	"ontology/effect"
	"ontology/journal"
)

// 四类拒绝原因，可用 errors.Is 区分，优先级按声明顺序。
var (
	ErrInvalidArg = errors.New("saga: 参数非法")
	ErrInstance   = errors.New("saga: 实例已存在或不存在")
	ErrState      = errors.New("saga: 状态不符")
	ErrStep       = errors.New("saga: 步骤不符")
)

const (
	maxSteps  = 64
	maxBudget = 10
)

// Manager 管理全部 Saga 实例。不同实例可并发，
// 同一实例的操作等价于某个串行顺序。
type Manager struct {
	jr *journal.Journal
	ef *effect.Table

	mu   sync.Mutex
	inst map[string]*instance

	lastReads atomic.Int64 // 非导出计数器：最近一次 Recover 读取的记录数
}

// instance 是单个 Saga 实例的内存状态。
type instance struct {
	mu     sync.Mutex
	n, p   int
	budget int

	inflight int  // 在途步骤下标，-1 表示无在途
	comp     bool // 在途是否为补偿（CI）
}

// New 返回基于给定日志与幂等表的管理器。
func New(jr *journal.Journal, ef *effect.Table) *Manager {
	return &Manager{jr: jr, ef: ef, inst: make(map[string]*instance)}
}

// Begin 创建实例：id 非空，1≤n≤64，0≤p<n，0≤B≤10。日志恰为 [B]。
func (m *Manager) Begin(id string, n, p, budget int) error {
	if id == "" || n < 1 || n > maxSteps || p < 0 || p >= n ||
		budget < 0 || budget > maxBudget {
		return ErrInvalidArg
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.inst[id]; ok {
		return ErrInstance
	}
	m.jr.Append(id, journal.Record{Kind: journal.KindBegin})
	m.inst[id] = &instance{n: n, p: p, budget: budget, inflight: -1}
	return nil
}

// get 取出实例并加锁，调用方负责解锁。
func (m *Manager) get(id string) (*instance, error) {
	if id == "" {
		return nil, ErrInvalidArg
	}
	m.mu.Lock()
	ins, ok := m.inst[id]
	m.mu.Unlock()
	if !ok {
		return nil, ErrInstance
	}
	ins.mu.Lock()
	return ins, nil
}

// checkStep 校验下标范围。
func (ins *instance) checkStep(i int) error {
	if i < 0 || i >= ins.n {
		return ErrInvalidArg
	}
	return nil
}

// plan 读取当前日志并推导计划（调用方须持有实例锁）。
func (m *Manager) plan(id string, ins *instance) Plan {
	return planOf(m.jr.Records(id), ins.n, ins.p, ins.budget)
}

// Intent 登记前滚意图：写 I(i) 并登记 (id,i,fwd)。要求计划为前滚 i。
func (m *Manager) Intent(id string, i int) error {
	ins, err := m.get(id)
	if err != nil {
		return err
	}
	defer ins.mu.Unlock()
	if err := ins.checkStep(i); err != nil {
		return err
	}
	if ins.inflight >= 0 {
		return ErrState
	}
	pl := m.plan(id, ins)
	if pl.Kind != PlanForward {
		return ErrState
	}
	if pl.Step != i {
		return ErrStep
	}
	m.jr.Append(id, journal.Record{Kind: journal.KindIntent, Step: i})
	m.ef.Register(effect.Key{ID: id, Step: i, Phase: effect.Fwd})
	ins.inflight, ins.comp = i, false
	return nil
}

// Done 报告步骤 i 成功：写 D(i)。要求同下标的 I 在途。
func (m *Manager) Done(id string, i int) error {
	return m.finish(id, i, false, journal.Transient)
}

// Fail 报告步骤 i 失败：写 F(i,k)。要求同下标的 I 在途。
func (m *Manager) Fail(id string, i int, k journal.FailKind) error {
	return m.finish(id, i, true, k)
}

// finish 是 Done/Fail 的公共实现。
func (m *Manager) finish(id string, i int, failed bool, k journal.FailKind) error {
	ins, err := m.get(id)
	if err != nil {
		return err
	}
	defer ins.mu.Unlock()
	if err := ins.checkStep(i); err != nil {
		return err
	}
	if ins.inflight < 0 || ins.comp {
		return ErrState
	}
	if ins.inflight != i {
		return ErrStep
	}
	rec := journal.Record{Kind: journal.KindDone, Step: i}
	if failed {
		rec = journal.Record{Kind: journal.KindFail, Step: i, Fail: k}
	}
	m.jr.Append(id, rec)
	ins.inflight = -1
	return nil
}
