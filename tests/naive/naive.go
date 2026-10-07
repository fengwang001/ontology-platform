// Package naive 是生命周期状态机子系统的独立朴素参考模型。
//
// 它与 ontology 包没有任何代码共享：内部用切片保存全部转移关系与全部钩子，
// 每次判定都线性遍历全部声明，语义上「简单且显然正确」，
// 专供差分测试对照优化实现（哈希索引）的结果。
package naive

// Reason 是朴素模型的四类拒绝原因，字符串值刻意与本体包解耦。
type Reason string

const (
	ReasonOK          Reason = "ok"
	ReasonBadArgument Reason = "invalid_argument"
	ReasonTerminal    Reason = "terminal_state"
	ReasonNotAllowed  Reason = "transition_not_allowed"
	ReasonHookFailed  Reason = "hook_failed"
)

// Kind 区分两类钩子。
type Kind string

const (
	KindTransition Kind = "transition"
	KindEntry      Kind = "entry"
)

const (
	SemanticCommitOnSuccess   = "commit_on_success"
	SemanticCommitImmediately = "commit_immediately"
)

// HookSpec 是一条朴素钩子声明，保存在大切片中等待每次全量扫描。
type HookSpec struct {
	ID       string
	Kind     Kind
	From     string // KindTransition 时有效
	Stage    string // 目标阶段（两类共用）
	Semantic string
	// Fails 是确定性的校验谓词：同一输入在任何世界中结论必须一致。
	Fails      func(instanceID, from, to string) bool
	FailDetail string
}

// Edge 是一条允许的直接转移。
type Edge struct{ From, To string }

// FireEntry 记录一次钩子触发事实。
type FireEntry struct {
	HookID      string
	Compensated bool
}

// SideEffectEntry 记录钩子副作用日志（用于验证提交语义）。
type SideEffectEntry struct {
	HookID      string
	Compensated bool
}

// TypeDecl 是朴素的对象类型声明。
type TypeDecl struct {
	Stages   []string
	Edges    []Edge
	Terminal []string
	Hooks    []HookSpec
}

func (d *TypeDecl) hasStage(s string) bool {
	for _, x := range d.Stages {
		if x == s {
			return true
		}
	}
	return false
}

func (d *TypeDecl) isTerminal(s string) bool {
	for _, x := range d.Terminal {
		if x == s {
			return true
		}
	}
	return false
}

// allows 线性遍历全部声明的转移关系。
func (d *TypeDecl) allows(from, to string) bool {
	for _, e := range d.Edges {
		if e.From == from && e.To == to {
			return true
		}
	}
	return false
}

// resolve 线性遍历全部已注册钩子，按「具体转移钩子（注册序）先于进入钩子（注册序）」排列。
// 自转移 (s,s) 的具体钩子仅在显式声明了 (s,s) 时命中；进入钩子只看目标阶段。
func (d *TypeDecl) resolve(from, to string) []HookSpec {
	var out []HookSpec
	for _, h := range d.Hooks { // 第一遍：具体转移钩子
		if h.Kind == KindTransition && h.From == from && h.Stage == to {
			out = append(out, h)
		}
	}
	for _, h := range d.Hooks { // 第二遍：进入钩子
		if h.Kind == KindEntry && h.Stage == to {
			out = append(out, h)
		}
	}
	return out
}

// ResolveBench 导出解析逻辑供基准测试使用（两次全量遍历）。
func (d *TypeDecl) ResolveBench(from, to string) []HookSpec { return d.resolve(from, to) }

// Instance 是朴素模型中的对象实例。
type Instance struct {
	ID         string
	Current    string
	History    [][2]string
	FireLog    []FireEntry
	SideEffect []SideEffectEntry
}

// Model 是朴素模型本身：无并发优化，调用方负责串行调用。
type Model struct {
	decl *TypeDecl
	inst map[string]*Instance
}

// NewModel 基于类型声明创建朴素模型。
func NewModel(decl *TypeDecl) *Model {
	return &Model{decl: decl, inst: make(map[string]*Instance)}
}

// Create 创建实例，initial 未声明时返回参数非法。
func (m *Model) Create(id, initial string) Reason {
	if id == "" || !m.decl.hasStage(initial) {
		return ReasonBadArgument
	}
	if _, ok := m.inst[id]; ok {
		return ReasonBadArgument
	}
	m.inst[id] = &Instance{ID: id, Current: initial}
	return ReasonOK
}

// Result 是一次转移的完整可观测结果。
type Result struct {
	Reason     Reason
	FailedHook string
	From       string
	To         string
	Fired      []string
	Current    string
	History    [][2]string
	FireLog    []FireEntry
	SideEffect []SideEffectEntry
}

// Transition 按与本体包完全相同的四段优先级执行一次转移。
func (m *Model) Transition(id, to string) Result {
	in := m.inst[id]
	snapshot := func(r Reason, failedHook string) Result {
		res := Result{Reason: r, FailedHook: failedHook, To: to, Current: "", FireLog: nil, SideEffect: nil}
		if in != nil {
			res.From = in.Current
			res.Current = in.Current
			res.History = append([][2]string(nil), in.History...)
			res.FireLog = append([]FireEntry(nil), in.FireLog...)
			res.SideEffect = append([]SideEffectEntry(nil), in.SideEffect...)
		}
		return res
	}

	if in == nil || !m.decl.hasStage(to) {
		return snapshot(ReasonBadArgument, "")
	}
	from := in.Current
	if m.decl.isTerminal(from) {
		return snapshot(ReasonTerminal, "")
	}
	if !m.decl.allows(from, to) {
		return snapshot(ReasonNotAllowed, "")
	}

	hooks := m.decl.resolve(from, to)
	type pendingEffect struct {
		h    HookSpec
		side int // 该钩子本次触发在 SideEffect 中的条目下标
	}
	var pending []pendingEffect
	for _, h := range hooks {
		in.FireLog = append(in.FireLog, FireEntry{HookID: h.ID})
		// 钩子触发即留下一条校验日志副作用。
		sideIdx := len(in.SideEffect)
		in.SideEffect = append(in.SideEffect, SideEffectEntry{HookID: h.ID})
		if h.Fails(id, from, to) {
			// 失败：逆序撤销声明为随转移提交的副作用；立即提交的保留。
			for i := len(pending) - 1; i >= 0; i-- {
				pe := pending[i]
				in.SideEffect[pe.side].Compensated = true // 精确撤销本次触发的那条
				in.SideEffect = append(in.SideEffect, SideEffectEntry{HookID: pe.h.ID, Compensated: true})
				in.FireLog = append(in.FireLog, FireEntry{HookID: pe.h.ID, Compensated: true})
			}
			return snapshot(ReasonHookFailed, h.ID)
		}
		if h.Semantic == SemanticCommitOnSuccess {
			pending = append(pending, pendingEffect{h: h, side: sideIdx})
		}
	}

	in.Current = to
	in.History = append(in.History, [2]string{from, to})
	res := snapshot(ReasonOK, "")
	res.Fired = nil
	for _, h := range hooks {
		res.Fired = append(res.Fired, h.ID)
	}
	return res
}

// Snapshot 返回实例当前全部可观测状态的深拷贝。
func (m *Model) Snapshot(id string) (Result, bool) {
	in, ok := m.inst[id]
	if !ok {
		return Result{}, false
	}
	return Result{
		Reason:     ReasonOK,
		Current:    in.Current,
		History:    append([][2]string(nil), in.History...),
		FireLog:    append([]FireEntry(nil), in.FireLog...),
		SideEffect: append([]SideEffectEntry(nil), in.SideEffect...),
	}, true
}
