package lifecycle

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"ontology/hooks"
	"ontology/lcerr"
)

// HistoryEntry 记录一次成功转移的阶段变化与钩子触发情况。
type HistoryEntry struct {
	// Seq 是该 Machine 上成功转移的全局单调序号。
	Seq uint64
	// From 是本次转移在串行顺序中实际所处的起始阶段。
	From Phase
	// To 是目标阶段。
	To Phase
	// Hooks 按实际触发顺序记录钩子名称（具体转移钩子在前，
	// 目标阶段进入钩子在后）。
	Hooks []string
}

// instance 是对象实例的运行时状态。mu 保护整个
// 「读取当前阶段 -> 校验 -> 执行钩子 -> 更新阶段」临界区，
// 使并发转移等价于按某个全局串行顺序逐一应用。
type instance struct {
	mu      sync.Mutex
	phase   Phase
	history []HistoryEntry
	journal []hooks.SideEffect
}

// Machine 是某个对象类型的生命周期状态机，管理该类型的全部实例。
type Machine struct {
	schema   *Schema
	registry *hooks.Registry

	mu        sync.RWMutex
	instances map[string]*instance
	seq       atomic.Uint64
}

// NewMachine 构造状态机；registry 为 nil 时使用空注册表（无钩子）。
func NewMachine(schema *Schema, registry *hooks.Registry) *Machine {
	if registry == nil {
		registry = hooks.NewRegistry()
	}
	return &Machine{
		schema:    schema,
		registry:  registry,
		instances: make(map[string]*instance),
	}
}

const (
	opInstantiate = "lifecycle.Instantiate"
	opTransition  = "lifecycle.Transition"
)

// Instantiate 以初始阶段创建一个对象实例。
func (m *Machine) Instantiate(id string) error {
	if id == "" {
		return lcerr.New(lcerr.KindInvalidArgument, opInstantiate, "instance id must not be empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, dup := m.instances[id]; dup {
		return lcerr.New(lcerr.KindInvalidArgument, opInstantiate,
			fmt.Sprintf("instance %q already exists", id))
	}
	m.instances[id] = &instance{phase: m.schema.initial}
	return nil
}

// pendingEffect 是一条暂存的钩子副作用及其提交语义。
type pendingEffect struct {
	effect hooks.SideEffect
	commit bool // true 表示 AutoCommit，转移被拒绝时仍保留
}

// Transition 请求把实例 instanceID 从当前阶段转移到目标阶段 to。
//
// 判定顺序（只报告第一个命中的拒绝原因）：
//  1. 参数非法：实例不存在、目标阶段未声明；
//  2. 起始阶段为终态：不允许任何转出，钩子不触发；
//  3. 转移关系未声明；
//  4. 钩子校验失败：先执行具体转移钩子，再执行目标阶段进入钩子。
//
// 任何拒绝都不会改变实例当前阶段；钩子副作用按各钩子声明的
// 提交语义决定保留（AutoCommit）或撤销（RollbackOnFailure）。
func (m *Machine) Transition(ctx context.Context, instanceID string, to Phase) error {
	m.mu.RLock()
	inst, ok := m.instances[instanceID]
	m.mu.RUnlock()
	if !ok {
		return lcerr.New(lcerr.KindInvalidArgument, opTransition,
			fmt.Sprintf("instance %q not found", instanceID))
	}
	if !m.schema.HasPhase(to) {
		return lcerr.New(lcerr.KindInvalidArgument, opTransition,
			fmt.Sprintf("target phase %q not declared", to))
	}

	inst.mu.Lock()
	defer inst.mu.Unlock()

	from := inst.phase
	if m.schema.IsTerminal(from) {
		return lcerr.New(lcerr.KindTerminalSource, opTransition,
			fmt.Sprintf("source phase %q is terminal", from))
	}
	if !m.schema.Allows(from, to) {
		return lcerr.New(lcerr.KindTransitionNotAllowed, opTransition,
			fmt.Sprintf("transition %q -> %q not allowed", from, to))
	}

	transitionHooks, enterHooks := m.registry.Resolve(from, to)
	ordered := make([]hooks.Hook, 0, len(transitionHooks)+len(enterHooks))
	ordered = append(ordered, transitionHooks...)
	ordered = append(ordered, enterHooks...)

	var pending []pendingEffect
	fired := make([]string, 0, len(ordered))
	for _, h := range ordered {
		tc := hooks.NewContext(instanceID, from, to, h.Name())
		err := h.Validate(ctx, tc)
		fired = append(fired, h.Name())
		commit := h.Semantics() == hooks.AutoCommit
		for _, e := range tc.Effects() {
			pending = append(pending, pendingEffect{effect: e, commit: commit})
		}
		if err != nil {
			// 转移整体不生效：阶段保持不变；已触发钩子中声明
			// AutoCommit 的副作用保留，其余随转移回滚。
			for _, p := range pending {
				if p.commit {
					inst.journal = append(inst.journal, p.effect)
				}
			}
			return lcerr.Wrap(lcerr.KindHookFailed, opTransition,
				fmt.Sprintf("hook %q rejected transition %q -> %q", h.Name(), from, to), err)
		}
	}

	for _, p := range pending {
		inst.journal = append(inst.journal, p.effect)
	}
	inst.phase = to
	inst.history = append(inst.history, HistoryEntry{
		Seq:   m.seq.Add(1),
		From:  from,
		To:    to,
		Hooks: fired,
	})
	return nil
}

// PhaseOf 返回实例当前所处阶段。
func (m *Machine) PhaseOf(instanceID string) (Phase, error) {
	inst, err := m.lookup(instanceID)
	if err != nil {
		return "", err
	}
	inst.mu.Lock()
	defer inst.mu.Unlock()
	return inst.phase, nil
}

// History 返回实例阶段变化历史的拷贝。
func (m *Machine) History(instanceID string) ([]HistoryEntry, error) {
	inst, err := m.lookup(instanceID)
	if err != nil {
		return nil, err
	}
	inst.mu.Lock()
	defer inst.mu.Unlock()
	out := make([]HistoryEntry, len(inst.history))
	copy(out, inst.history)
	return out, nil
}

// Journal 返回实例已提交的钩子副作用日志的拷贝。
func (m *Machine) Journal(instanceID string) ([]hooks.SideEffect, error) {
	inst, err := m.lookup(instanceID)
	if err != nil {
		return nil, err
	}
	inst.mu.Lock()
	defer inst.mu.Unlock()
	out := make([]hooks.SideEffect, len(inst.journal))
	copy(out, inst.journal)
	return out, nil
}

func (m *Machine) lookup(instanceID string) (*instance, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	inst, ok := m.instances[instanceID]
	if !ok {
		return nil, lcerr.New(lcerr.KindInvalidArgument, opTransition,
			fmt.Sprintf("instance %q not found", instanceID))
	}
	return inst, nil
}
