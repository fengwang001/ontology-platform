package actionguard

// 本文件定义动作校验机制的类型基础。
//
// 设计要点：前置条件与后置条件都被建模为“字面量（literal）的合取”，
// 字面量引用一个原子模板（AtomSpec）。模板中的实参要么是常量槽，
// 要么是输入变量槽。这样：
//   - 执行期：用具体输入把模板实例化（ground），再到对应快照上求值；
//   - 定义期：仅凭模板之间的可合一性（unification）即可判定同一个阶段内
//     是否存在“一个条件要求真、另一个条件要求假”的结构性互斥，
//     开销只与声明的字面量数量有关，与历史调用次数无关。

// Arg 是原子模板中的槽位实参。
type Arg interface{ isArg() }

// ConstArg 是常量槽（动作声明时即固定，例如链接关系名）。
type ConstArg string

// VarArg 是变量槽（执行期由输入绑定，例如金额、账户 ID）。
type VarArg string

func (ConstArg) isArg() {}
func (VarArg) isArg()   {}

// AtomSpec 是原子判定模板，例如 (balanceGte, from, amount)。
type AtomSpec struct {
	Kind string
	Args []Arg
}

// Literal 是“原子模板 + 极性”的字面量。
type Literal struct {
	Spec   AtomSpec
	Expect bool
}

// Snapshot 是不可变的只读状态视图。
//
// 前置条件拿到的是执行前已持久化状态的深拷贝快照；
// 后置条件拿到的是“执行前快照 + 本次最终写入计划”投影出的快照。
// 投影快照不提供任何渠道回看执行前的中间状态，因此后置阶段
// 在结构上不可能重新评估前置阶段已经通过的条件。
type Snapshot interface {
	Atom(grounded string) bool
	Attr(objID, attr string) (string, bool)
	Version(objID string) int64
	Revoked(objID string) bool
}

// Plan 是一次调用计算出的最终写入计划（尚未提交，可整体放弃）。
//
// 计划使用“最终值”映射表达：同一次调用内对同一对象/链接的多次
// 写入会折叠为最后一次的值，因此任何中间状态都不会进入计划，
// 后置校验看到的必然是最终计划。
type Plan struct {
	ObjectAttrs   map[string]map[string]string // objID -> attr -> finalValue
	ObjectRevoked map[string]bool              // objID -> 最终是否撤销
	Links         map[string]bool              // linkKey -> finalPresent
}

// PlanBuilder 供 Planner 累积写入；重复写入直接覆盖，语义即“最终计划”。
type PlanBuilder struct{ p *Plan }

func NewPlanBuilder() *PlanBuilder {
	return &PlanBuilder{p: &Plan{
		ObjectAttrs:   map[string]map[string]string{},
		ObjectRevoked: map[string]bool{},
		Links:         map[string]bool{},
	}}
}

func (b *PlanBuilder) SetAttr(objID, attr, value string) *PlanBuilder {
	m := b.p.ObjectAttrs[objID]
	if m == nil {
		m = map[string]string{}
		b.p.ObjectAttrs[objID] = m
	}
	m[attr] = value
	return b
}

func (b *PlanBuilder) SetLink(key string, present bool) *PlanBuilder {
	b.p.Links[key] = present
	return b
}

func (b *PlanBuilder) Revoke(objID string) *PlanBuilder {
	b.p.ObjectRevoked[objID] = true
	return b
}

func (b *PlanBuilder) Build() *Plan { return b.p }

// Evidence 记录某个字面量的校验依据：实例化后的原子与其观测值。
type Evidence struct {
	GroundedAtom string
	Observed     bool
	Expect       bool
}

// Planner 依据输入与执行前快照纯函数式地生成写入计划。
// Planner 不接收 Store，无法产生任何可观察副作用。
type Planner func(input map[string]any, snap Snapshot) (*Plan, error)

// ConditionClause 是一个命名条件：若干字面量的合取。
type ConditionClause struct {
	Name string
	Lits []Literal
}

// ConditionFactory 依据输入构造本阶段的全部命名条件。
// 输入相关的量以 VarArg 出现在字面量模板中，因此注册期即使
// 传入 nil 输入也能完成结构性矛盾检查。
type ConditionFactory func(input map[string]any) ([]ConditionClause, error)

// Action 是一个动作声明。
type Action struct {
	Type string
	// Pre / Post 返回该动作在给定输入下的全部字面量（按声明顺序）。
	Pre  ConditionFactory
	Post ConditionFactory
	Plan Planner
}

// OutcomeClass 对外暴露的四类执行结果。
type OutcomeClass int

const (
	OutcomeAccepted OutcomeClass = iota
	OutcomePreRejected
	OutcomePostRejected
	OutcomeObjectRevoked
)

func (c OutcomeClass) String() string {
	switch c {
	case OutcomeAccepted:
		return "accepted"
	case OutcomePreRejected:
		return "pre_rejected"
	case OutcomePostRejected:
		return "post_rejected"
	case OutcomeObjectRevoked:
		return "object_revoked"
	}
	return "unknown"
}

// Outcome 一次执行尝试的结论。
type Outcome struct {
	Class      OutcomeClass
	ActionType string
	CallID     string
	FailedPre  []string // 前置阶段：全部不通过条件
	FailedPost string   // 后置阶段：仅决定性条件
	Detail     string
	Accepted   bool
}

// AuditEntry 每次校验的记录：输入、校验依据与结论。
type AuditEntry struct {
	CallID     string
	ActionType string
	Phase      string // "pre" | "post" | "revocation"
	Condition  string
	Input      string
	Basis      string
	Result     string // "pass" | "fail"
}
