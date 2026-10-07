// Package naive 提供独立维护全部转移关系与钩子注册表、
// 每次遍历全部声明判断命中的朴素参考模型，用于差分测试。
//
// 该模型刻意不使用任何索引：每次转移都线性遍历全部阶段、全部转移关系、
// 全部已注册钩子来判断命中，以此作为优化实现（ontology 包）的对照基准。
package naive

import (
	"fmt"
	"sync"
)

// Stage 是生命周期阶段的标识。
type Stage = string

// Edge 是一条允许的有向转移关系。
type Edge struct {
	From Stage
	To   Stage
}

// HookKind 区分两类钩子。
type HookKind int

const (
	HookTransition HookKind = iota
	HookEnter
)

// CommitSemantics 声明钩子副作用在转移回退时的提交语义。
type CommitSemantics int

const (
	RollbackOnFailure CommitSemantics = iota
	CommitOnFailure
)

// Hook 是朴素模型中的钩子定义。
type Hook struct {
	Name   string
	Kind   HookKind
	From   Stage
	To     Stage
	Commit CommitSemantics
	Fn     func(ctx *Context) error
}

// Context 是钩子执行上下文。
type Context struct {
	InstanceID string
	From       Stage
	To         Stage
	emit       func(detail string)
}

// Emit 记录一条钩子副作用。
func (c *Context) Emit(detail string) { c.emit(detail) }

// ErrorKind 区分四类拒绝原因。
type ErrorKind int

const (
	InvalidArgument ErrorKind = iota
	TerminalStage
	TransitionNotAllowed
	HookFailed
)

// HookFiring 是一次钩子触发记录。
type HookFiring struct {
	Hook   string
	Kind   HookKind
	Failed bool
}

// SideEffect 是钩子产生的一条记录副作用。
type SideEffect struct {
	Hook       string
	Detail     string
	RolledBack bool
}

// Record 是一次转移请求的结果记录。
type Record struct {
	From  Stage
	To    Stage
	Fired []HookFiring
	Kind  ErrorKind
	OK    bool
}

// Model 是朴素参考模型：全部声明保存在切片中，每次转移全量遍历。
type Model struct {
	mu       sync.Mutex
	stages   []Stage
	edges    []Edge
	terminal []Stage
	hooks    []*Hook

	stage       Stage
	history     []Stage
	trace       []Record
	sideEffects []SideEffect
}

// New 构建朴素模型实例。
func New(stages []Stage, edges []Edge, terminals []Stage, hooks []*Hook, initial Stage) *Model {
	return &Model{
		stages:   append([]Stage(nil), stages...),
		edges:    append([]Edge(nil), edges...),
		terminal: append([]Stage(nil), terminals...),
		hooks:    append([]*Hook(nil), hooks...),
		stage:    initial,
		history:  []Stage{initial},
	}
}

func (m *Model) hasStage(s Stage) bool {
	for _, x := range m.stages {
		if x == s {
			return true
		}
	}
	return false
}

func (m *Model) isTerminal(s Stage) bool {
	for _, x := range m.terminal {
		if x == s {
			return true
		}
	}
	return false
}

func (m *Model) allows(from, to Stage) bool {
	for _, e := range m.edges {
		if e.From == from && e.To == to {
			return true
		}
	}
	return false
}

// hitHooks 遍历全部已注册钩子判断命中：先收集具体转移钩子，再收集进入钩子。
func (m *Model) hitHooks(from, to Stage) []*Hook {
	var out []*Hook
	for _, h := range m.hooks {
		if h.Kind == HookTransition && h.From == from && h.To == to {
			out = append(out, h)
		}
	}
	for _, h := range m.hooks {
		if h.Kind == HookEnter && h.To == to {
			out = append(out, h)
		}
	}
	return out
}

// Transition 执行一次转移，语义与 ontology.ObjectType.Transition 保持一致。
func (m *Model) Transition(to Stage) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	from := m.stage
	rec := Record{From: from, To: to}

	if !m.hasStage(to) {
		rec.Kind = InvalidArgument
		m.trace = append(m.trace, rec)
		return fmt.Errorf("naive: invalid_argument: %q", to)
	}
	if m.isTerminal(from) {
		rec.Kind = TerminalStage
		m.trace = append(m.trace, rec)
		return fmt.Errorf("naive: terminal_stage: %q", from)
	}
	if !m.allows(from, to) {
		rec.Kind = TransitionNotAllowed
		m.trace = append(m.trace, rec)
		return fmt.Errorf("naive: transition_not_allowed: %q->%q", from, to)
	}

	hooks := m.hitHooks(from, to)
	base := len(m.sideEffects)
	fired := make([]*Hook, 0, len(hooks))
	for _, h := range hooks {
		hook := h
		ctx := &Context{
			From: from,
			To:   to,
			emit: func(detail string) {
				m.sideEffects = append(m.sideEffects, SideEffect{Hook: hook.Name, Detail: detail})
			},
		}
		firing := HookFiring{Hook: hook.Name, Kind: hook.Kind}
		err := hook.Fn(ctx)
		fired = append(fired, hook)
		if err != nil {
			firing.Failed = true
			rec.Fired = append(rec.Fired, firing)
			commit := make(map[string]CommitSemantics, len(fired))
			for _, fh := range fired {
				commit[fh.Name] = fh.Commit
			}
			for k := base; k < len(m.sideEffects); k++ {
				if commit[m.sideEffects[k].Hook] == RollbackOnFailure {
					m.sideEffects[k].RolledBack = true
				}
			}
			rec.Kind = HookFailed
			m.trace = append(m.trace, rec)
			return fmt.Errorf("naive: hook_failed: %s", hook.Name)
		}
		rec.Fired = append(rec.Fired, firing)
	}

	m.stage = to
	m.history = append(m.history, to)
	rec.OK = true
	m.trace = append(m.trace, rec)
	return nil
}

// Stage 返回当前阶段。
func (m *Model) Stage() Stage {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stage
}

// History 返回阶段变化轨迹。
func (m *Model) History() []Stage {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Stage(nil), m.history...)
}

// Trace 返回全部转移请求记录。
func (m *Model) Trace() []Record {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Record(nil), m.trace...)
}

// SideEffects 返回钩子记录副作用。
func (m *Model) SideEffects() []SideEffect {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]SideEffect(nil), m.sideEffects...)
}
