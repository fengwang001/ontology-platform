package ontology

import (
	"fmt"
	"sync"
)

// Stage 是生命周期阶段的标识。
type Stage string

// Edge 是一条允许的有向转移关系（允许环与自环）。
type Edge struct {
	From Stage
	To   Stage
}

// ObjectTypeDef 声明一个对象类型的生命周期：
// 有限阶段集合、允许的有向转移关系、终态集合。
type ObjectTypeDef struct {
	Name      string
	Stages    []Stage
	Edges     []Edge
	Terminals []Stage
}

// ObjectType 负责生命周期状态机定义与转移执行。
type ObjectType struct {
	name     string
	stages   map[Stage]bool
	allowed  map[Edge]bool
	terminal map[Stage]bool
	hooks    *HookRegistry
}

// NewObjectType 校验声明并构建对象类型。
func NewObjectType(def ObjectTypeDef) (*ObjectType, error) {
	if def.Name == "" {
		return nil, fmt.Errorf("ontology: object type name is empty")
	}
	t := &ObjectType{
		name:     def.Name,
		stages:   make(map[Stage]bool, len(def.Stages)),
		allowed:  make(map[Edge]bool, len(def.Edges)),
		terminal: make(map[Stage]bool, len(def.Terminals)),
		hooks:    NewHookRegistry(),
	}
	for _, s := range def.Stages {
		if t.stages[s] {
			return nil, fmt.Errorf("ontology: duplicate stage %q", s)
		}
		t.stages[s] = true
	}
	for _, e := range def.Edges {
		if !t.stages[e.From] || !t.stages[e.To] {
			return nil, fmt.Errorf("ontology: edge %q->%q references undeclared stage", e.From, e.To)
		}
		t.allowed[e] = true
	}
	for _, s := range def.Terminals {
		if !t.stages[s] {
			return nil, fmt.Errorf("ontology: terminal stage %q is not declared", s)
		}
		t.terminal[s] = true
	}
	return t, nil
}

// Name 返回对象类型名。
func (t *ObjectType) Name() string { return t.name }

// Hooks 返回该对象类型的钩子注册表。
func (t *ObjectType) Hooks() *HookRegistry { return t.hooks }

// HasStage 报告阶段是否已声明。
func (t *ObjectType) HasStage(s Stage) bool { return t.stages[s] }

// IsTerminal 报告阶段是否为终态。
func (t *ObjectType) IsTerminal(s Stage) bool { return t.terminal[s] }

// Allows 报告转移关系是否被声明允许。
func (t *ObjectType) Allows(from, to Stage) bool { return t.allowed[Edge{From: from, To: to}] }

// HookFiring 是一次钩子触发的记录。
type HookFiring struct {
	Hook   string
	Kind   HookKind
	Failed bool
}

// SideEffect 是钩子产生的一条记录副作用。
type SideEffect struct {
	Hook       string
	Detail     string
	RolledBack bool // 转移失败且钩子声明 RollbackOnFailure 时为 true
}

// TransitionRecord 是一次转移请求的完整结果记录。
type TransitionRecord struct {
	Seq   int
	From  Stage
	To    Stage
	Fired []HookFiring // 按触发顺序
	Kind  ErrorKind    // 仅失败时有意义
	OK    bool
}

// Instance 是一个对象实例的生命周期状态。
// 所有转移在实例级互斥锁下串行执行，因此并发请求的最终效果
// 等价于按某个全局串行顺序逐一应用。
type Instance struct {
	mu          sync.Mutex
	id          string
	typ         *ObjectType
	stage       Stage
	history     []Stage // history[0] 为初始阶段，之后每次成功转移追加一项
	trace       []TransitionRecord
	sideEffects []SideEffect
}

// NewInstance 以指定初始阶段创建实例。
func (t *ObjectType) NewInstance(id string, initial Stage) (*Instance, error) {
	if !t.stages[initial] {
		return nil, &Error{Kind: ErrInvalidArgument, ObjectType: t.name, InstanceID: id, To: initial}
	}
	return &Instance{id: id, typ: t, stage: initial, history: []Stage{initial}}, nil
}

// ID 返回实例标识。
func (i *Instance) ID() string { return i.id }

// Stage 返回实例当前所处阶段。
func (i *Instance) Stage() Stage {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.stage
}

// History 返回阶段变化轨迹（含初始阶段）。
func (i *Instance) History() []Stage {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]Stage(nil), i.history...)
}

// Trace 返回全部转移请求的记录（按串行应用顺序）。
func (i *Instance) Trace() []TransitionRecord {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]TransitionRecord(nil), i.trace...)
}

// SideEffects 返回钩子产生的记录副作用。
func (i *Instance) SideEffects() []SideEffect {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]SideEffect(nil), i.sideEffects...)
}

// Transition 尝试把实例转移到目标阶段。
//
// 拒绝原因按以下次序只报第一个命中的：
//  1. ErrInvalidArgument 实例不存在（nil 或属于其他对象类型）、目标阶段未声明
//  2. ErrTerminalStage 起始阶段为终态（优先于钩子，钩子不会被触发）
//  3. ErrTransitionNotAllowed 转移关系未声明
//  4. ErrHookFailed 钩子校验失败（转移整体不生效，阶段保持转移前的值）
//
// 被拒绝的转移不改变实例当前阶段。
func (t *ObjectType) Transition(inst *Instance, to Stage) error {
	if inst == nil || inst.typ != t {
		return newError(ErrInvalidArgument, t, inst, "", to)
	}
	inst.mu.Lock()
	defer inst.mu.Unlock()

	from := inst.stage
	rec := TransitionRecord{Seq: len(inst.trace), From: from, To: to}

	// 1. 参数非法：目标阶段未声明。
	if !t.stages[to] {
		rec.Kind = ErrInvalidArgument
		inst.trace = append(inst.trace, rec)
		return newError(ErrInvalidArgument, t, inst, from, to)
	}
	// 2. 起始阶段为终态：优先于钩子校验，钩子不得触发。
	if t.terminal[from] {
		rec.Kind = ErrTerminalStage
		inst.trace = append(inst.trace, rec)
		return newError(ErrTerminalStage, t, inst, from, to)
	}
	// 3. 转移关系未被允许。
	if !t.allowed[Edge{From: from, To: to}] {
		rec.Kind = ErrTransitionNotAllowed
		inst.trace = append(inst.trace, rec)
		return newError(ErrTransitionNotAllowed, t, inst, from, to)
	}

	// 4. 钩子校验：具体转移钩子先于目标阶段进入钩子。
	hooks := t.hooks.Resolve(from, to)
	sideEffectBase := len(inst.sideEffects)
	fired := make([]*Hook, 0, len(hooks))
	for _, h := range hooks {
		hook := h
		ctx := &HookContext{
			ObjectType: t.name,
			InstanceID: inst.id,
			From:       from,
			To:         to,
			emit: func(se SideEffect) {
				se.Hook = hook.Name
				inst.sideEffects = append(inst.sideEffects, se)
			},
		}
		firing := HookFiring{Hook: hook.Name, Kind: hook.Kind}
		err := hook.Fn(ctx)
		fired = append(fired, hook)
		if err != nil {
			firing.Failed = true
			rec.Fired = append(rec.Fired, firing)
			// 转移整体不生效；已触发钩子的记录副作用按其声明的提交语义处理。
			commit := make(map[string]CommitSemantics, len(fired))
			for _, fh := range fired {
				commit[fh.Name] = fh.Commit
			}
			for k := sideEffectBase; k < len(inst.sideEffects); k++ {
				if commit[inst.sideEffects[k].Hook] == RollbackOnFailure {
					inst.sideEffects[k].RolledBack = true
				}
			}
			rec.Kind = ErrHookFailed
			inst.trace = append(inst.trace, rec)
			terr := newError(ErrHookFailed, t, inst, from, to)
			terr.Hook = hook.Name
			terr.Cause = err
			return terr
		}
		rec.Fired = append(rec.Fired, firing)
	}

	// 全部钩子通过：提交转移。
	inst.stage = to
	inst.history = append(inst.history, to)
	rec.OK = true
	inst.trace = append(inst.trace, rec)
	return nil
}
