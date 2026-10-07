package chain

import "fmt"

// Params 是动作的输入参数集合。
type Params map[string]any

// FrameKey 唯一标识调用栈中的一次“待触发调用”：动作名 + 规范化输入。
type FrameKey struct {
	Action string
	Input  string
}

// Status 是一次内层（或顶层）调用在审计记录中的最终结论分类。
type Status int

const (
	// StatusCompleted 成功完成。
	StatusCompleted Status = iota
	// StatusPreFailed 内层前置条件未通过。
	StatusPreFailed
	// StatusPostFailed 内层后置效果/后置条件未通过（关键调用，导致整体放弃）。
	StatusPostFailed
	// StatusNonCriticalFailed 非关键调用失败，已记录，外层继续。
	StatusNonCriticalFailed
	// StatusSelfTriggerRejected 检测到自我触发，触发被拒绝。
	StatusSelfTriggerRejected
	// StatusAborted 因后代关键调用失败而被连带放弃的外层帧。
	StatusAborted
	// StatusDeclarationError 调用链声明期错误（如依赖非关键调用输出）。
	StatusDeclarationError
)

// ExecContext 是前置条件、效果函数与后置条件在运行期看到的只读上下文。
// 动作不能通过它任意触发子调用；所有子调用必须在 ChildSpec 中静态声明。
type ExecContext interface {
	// Input 返回当前帧自身的输入参数。
	Input() Params
	// State 返回当前可见状态视图（持久化状态 + 所有外层的中间写入计划）。
	State() *View
	// Output 返回本帧此前已完成的关键子调用按名字暴露的输出。
	Output(name string) (any, bool)
}

// ChildSpec 是一条对内层动作调用的静态声明。
type ChildSpec struct {
	// Name 被调用动作名。
	Name string
	// Critical 为 true 时该调用失败会导致整条链条已计算写入计划整体放弃。
	Critical bool
	// When 决定该子调用本次是否触发；返回 false 时完全不发生触发
	// （因此也不做自我触发检测与前置评估）。
	When func(parent Params, out func(name string) (any, bool)) bool
	// Bind 依据外层输入与此前关键子调用输出计算内层输入。
	Bind func(parent Params, out func(name string) (any, bool)) Params
	// Consumes 声明 Bind 以及外层效果函数依赖的此前子调用输出名列表。
	// 声明为非关键的子调用输出不允许出现在任何后继的 Consumes 中。
	Consumes []string
}

// Action 描述一个本体动作。
type Action struct {
	Name string
	// Pre 在进入时评估；只能看到进入视图（持久化 + 外层中间计划）。
	Pre func(ctx ExecContext) (pass bool, basis string)
	// Effect 计算本帧写入计划与输出；返回 error 视为后置效果失败。
	Effect func(ctx ExecContext) (ops []WriteOp, outputs map[string]any, basis string, err error)
	// Post 在本帧写入计划叠加后评估后置不变量；false 视为后置条件失败。
	Post func(ctx ExecContext, outputs map[string]any) (pass bool, basis string)
	// Children 是本帧按声明顺序触发的全部内层调用。
	Children []ChildSpec
	// EffectConsumes 声明 Effect 依赖的此前子调用输出。
	EffectConsumes []string
}

// Registry 是动作注册表，同时承担调用链条的声明期静态校验。
type Registry struct {
	actions map[string]*Action
}

// NewRegistry 创建空注册表。
func NewRegistry() *Registry {
	return &Registry{actions: make(map[string]*Action)}
}

// Register 注册一个动作。
func (r *Registry) Register(a *Action) error {
	if a == nil || a.Name == "" {
		return &declarationError{msg: "action must have a non-empty name"}
	}
	if _, dup := r.actions[a.Name]; dup {
		return &declarationError{msg: fmt.Sprintf("duplicate action %q", a.Name)}
	}
	seen := make(map[string]bool, len(a.Children))
	for i, ch := range a.Children {
		if ch.Name == "" {
			return &declarationError{msg: fmt.Sprintf("action %q child[%d] missing name", a.Name, i)}
		}
		if seen[ch.Name] {
			return &declarationError{msg: fmt.Sprintf("action %q declares duplicate child name %q", a.Name, ch.Name)}
		}
		seen[ch.Name] = true
		// Consumes 只能引用声明顺序上更早的子调用。
		for _, dep := range ch.Consumes {
			if !seen[dep] {
				return &declarationError{msg: fmt.Sprintf(
					"action %q child %q consumes %q which is not a preceding sibling",
					a.Name, ch.Name, dep)}
			}
		}
	}
	for _, dep := range a.EffectConsumes {
		if !seen[dep] {
			return &declarationError{msg: fmt.Sprintf(
				"action %q effect consumes %q which is not a declared child", a.Name, dep)}
		}
	}
	r.actions[a.Name] = a
	return nil
}

// Get 按名查找动作。
func (r *Registry) Get(name string) (*Action, bool) {
	a, ok := r.actions[name]
	return a, ok
}

// ValidateChain 以给定入口动作做一次声明期静态校验，
// 拦截依赖非关键调用输出等声明错误。
func (r *Registry) ValidateChain(entry string) error {
	if _, ok := r.actions[entry]; !ok {
		return &declarationError{msg: fmt.Sprintf("unknown entry action %q", entry)}
	}
	visited := map[string]bool{}
	var walk func(name string) error
	walk = func(name string) error {
		if visited[name] {
			return nil // 调用图中的环不是声明错误；具体输入的自我触发在运行期判定
		}
		visited[name] = true
		a, ok := r.actions[name]
		if !ok {
			return &declarationError{msg: fmt.Sprintf("action %q references unknown child", name)}
		}
		// criticality[name] = 该子调用是否为关键调用（声明顺序上更早的兄弟）。
		criticality := map[string]bool{}
		checkDeps := func(consumer, dep string) error {
			crit, declared := criticality[dep]
			if !declared {
				return &declarationError{msg: fmt.Sprintf(
					"action %q: %s consumes %q which is not a preceding sibling",
					name, consumer, dep)}
			}
			if !crit {
				// 非关键调用可能失败而外层继续，其输出不可被后继流程依赖，
				// 否则执行期将出现“拿不到预期输出”的不可解释结果。
				return &declarationError{msg: fmt.Sprintf(
					"action %q: %s consumes output of non-critical child %q; "+
						"a failed non-critical call must never look successful",
					name, consumer, dep)}
			}
			return nil
		}
		for i, ch := range a.Children {
			consumer := fmt.Sprintf("child[%d]=%q", i, ch.Name)
			for _, dep := range ch.Consumes {
				if err := checkDeps(consumer, dep); err != nil {
					return err
				}
			}
			criticality[ch.Name] = ch.Critical
		}
		for _, dep := range a.EffectConsumes {
			if err := checkDeps("effect", dep); err != nil {
				return err
			}
		}
		for _, ch := range a.Children {
			if err := walk(ch.Name); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(entry)
}
