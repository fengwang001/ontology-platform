// Package ontology：本文件负责生命周期状态机的定义与转移执行。
package ontology

import "sync"

// HistoryEntry 记录一次成功转移造成的阶段变化。
type HistoryEntry struct {
	From string
	To   string
}

// FireRecord 记录一次钩子触发（或补偿）事实，用于重放对照。
type FireRecord struct {
	InstanceID  string
	HookID      string
	From        string
	To          string
	Compensated bool
}

// ObjectType 声明有限生命周期阶段、允许转移关系（可有环）、终态与钩子表。
type ObjectType struct {
	name     string
	stages   map[string]struct{}
	allowed  map[edgeKey]struct{}
	terminal map[string]struct{}
	registry *HookRegistry
}

// NewObjectType 创建对象类型声明。
// stages 为有限阶段集合；allowed 为允许的直接转移（可有环、可有自环）；
// terminal 中的阶段为终态。引用了未声明阶段的转移/终态一律视为参数非法。
func NewObjectType(name string, stages []string, allowed [][2]string, terminal []string) (*ObjectType, error) {
	ot := &ObjectType{
		name:     name,
		stages:   make(map[string]struct{}),
		allowed:  make(map[edgeKey]struct{}),
		terminal: make(map[string]struct{}),
		registry: NewHookRegistry(),
	}
	for _, s := range stages {
		if s == "" {
			return nil, newError(ErrorInvalidArgument, "object type "+name+": stage name must not be empty")
		}
		ot.stages[s] = struct{}{}
	}
	for _, pair := range allowed {
		if _, ok := ot.stages[pair[0]]; !ok {
			return nil, newError(ErrorInvalidArgument, "object type "+name+": allowed edge references undeclared source stage "+pair[0])
		}
		if _, ok := ot.stages[pair[1]]; !ok {
			return nil, newError(ErrorInvalidArgument, "object type "+name+": allowed edge references undeclared target stage "+pair[1])
		}
		ot.allowed[edgeKey{from: pair[0], to: pair[1]}] = struct{}{}
	}
	for _, s := range terminal {
		if _, ok := ot.stages[s]; !ok {
			return nil, newError(ErrorInvalidArgument, "object type "+name+": terminal references undeclared stage "+s)
		}
		ot.terminal[s] = struct{}{}
	}
	return ot, nil
}

// Registry 返回该类型绑定的钩子注册表（不存在则懒创建）。
func (t *ObjectType) Registry() *HookRegistry { return t.registry }

// HasStage 判断阶段是否已声明。
func (t *ObjectType) HasStage(s string) bool {
	_, ok := t.stages[s]
	return ok
}

// IsTerminal 判断阶段是否被声明为终态。
func (t *ObjectType) IsTerminal(s string) bool {
	_, ok := t.terminal[s]
	return ok
}

// Allows 判断 from -> to 是否为声明的允许转移（哈希查表，O(1)）。
func (t *ObjectType) Allows(from, to string) bool {
	_, ok := t.allowed[edgeKey{from: from, to: to}]
	return ok
}

// Instance 是一个对象实例及其生命周期状态。
type Instance struct {
	ID      string
	ot      *ObjectType
	mu      sync.Mutex
	current string
	history []HistoryEntry
	fireLog []FireRecord
}

// Manager 管理实例的创建与串行化转移执行。
type Manager struct {
	mu        sync.Mutex
	instances map[string]*Instance
}

// NewManager 创建空实例管理器。
func NewManager() *Manager { return &Manager{instances: make(map[string]*Instance)} }

// CreateInstance 在指定初始阶段创建实例。
func (m *Manager) CreateInstance(ot *ObjectType, id, initial string) (*Instance, error) {
	if ot == nil {
		return nil, newError(ErrorInvalidArgument, "object type must not be nil")
	}
	if id == "" {
		return nil, newError(ErrorInvalidArgument, "instance id must not be empty")
	}
	if !ot.HasStage(initial) {
		return nil, newError(ErrorInvalidArgument, "initial stage undeclared: "+initial)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.instances[id]; exists {
		return nil, newError(ErrorInvalidArgument, "instance already exists: "+id)
	}
	in := &Instance{ID: id, ot: ot, current: initial}
	m.instances[id] = in
	return in, nil
}

// GetInstance 按 ID 查询实例（不存在返回 nil）。
func (m *Manager) GetInstance(id string) *Instance {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.instances[id]
}

// TransitionResult 是一次成功转移的结果。
type TransitionResult struct {
	From  string
	To    string
	Fired []string
}

// Transition 对实例执行一次带校验钩子的生命周期转移。
//
// 拒绝原因严格按以下次序只报第一个：
//  1. 参数非法（实例不存在 / 目标阶段未声明）
//  2. 起始阶段为终态（终态不可转出，包括转到自身；且优先于钩子，钩子不得触发）
//  3. 转移关系未被允许
//  4. 钩子校验失败
//
// 整个判定与执行在实例锁内完成，因此同一实例的并发请求等价于某种全局串行顺序，
// 每次转移的起始阶段以其在该串行顺序中的实际位置为准。
func (m *Manager) Transition(id, to string) (*TransitionResult, error) {
	in := m.GetInstance(id)

	// 第 1 优先级：参数非法。
	if in == nil {
		return nil, newError(ErrorInvalidArgument, "instance not found: "+id)
	}
	ot := in.ot
	if !ot.HasStage(to) {
		return nil, newError(ErrorInvalidArgument, "target stage undeclared: "+to)
	}

	in.mu.Lock()
	defer in.mu.Unlock()

	from := in.current

	// 第 2 优先级：起始阶段为终态。此时一个钩子都不得触发。
	if ot.IsTerminal(from) {
		return nil, newError(ErrorTerminal, "stage "+from+" is terminal; no outgoing transition allowed")
	}

	// 第 3 优先级：转移关系未被允许（自转移也必须显式声明）。
	if !ot.Allows(from, to) {
		return nil, newError(ErrorTransitionNotAllowed, "transition "+from+" -> "+to+" is not declared")
	}

	// 第 4 优先级：钩子。范围解析只触碰本次命中的单元（见 ResolveProbed）。
	hooks := ot.registry.Resolve(from, to)
	ctx := &TransitionContext{InstanceID: id, From: from, To: to}

	// 已成功执行、且声明为随转移提交的钩子；失败时逆序补偿。
	var committedPending []Hook
	fired := make([]string, 0, len(hooks))
	for _, h := range hooks {
		in.fireLog = append(in.fireLog, FireRecord{InstanceID: id, HookID: h.ID, From: from, To: to})
		fired = append(fired, h.ID)
		if err := h.Check(ctx); err != nil {
			// 转移整体不生效：阶段保持转移前值，不写 history。
			for i := len(committedPending) - 1; i >= 0; i-- {
				h := committedPending[i]
				if h.Compensate != nil {
					h.Compensate(ctx)
				}
				in.fireLog = append(in.fireLog, FireRecord{
					InstanceID: id, HookID: h.ID, From: from, To: to, Compensated: true,
				})
			}
			return nil, &LifecycleError{
				Code:   ErrorHookFailed,
				Reason: "hook " + h.ID + " rejected transition " + from + " -> " + to + ": " + err.Error(),
				HookID: h.ID,
			}
		}
		if h.Semantic == CommitOnSuccess {
			committedPending = append(committedPending, h)
		}
		// CommitImmediately 的副作用此处即落定，失败不补偿。
	}

	// 全部钩子通过后才唯一一次提交阶段变化，不存在「跑了一半」的中间阶段。
	in.current = to
	in.history = append(in.history, HistoryEntry{From: from, To: to})
	return &TransitionResult{From: from, To: to, Fired: fired}, nil
}

// Current 返回实例当前阶段。
func (in *Instance) Current() string {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.current
}

// History 返回实例成功转移轨迹的副本。
func (in *Instance) History() []HistoryEntry {
	in.mu.Lock()
	defer in.mu.Unlock()
	out := make([]HistoryEntry, len(in.history))
	copy(out, in.history)
	return out
}

// FireLog 返回实例钩子触发记录的副本。
func (in *Instance) FireLog() []FireRecord {
	in.mu.Lock()
	defer in.mu.Unlock()
	out := make([]FireRecord, len(in.fireLog))
	copy(out, in.fireLog)
	return out
}
