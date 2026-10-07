// Package ontology 实现本体平台的多租户命名空间权限继承覆盖模块。
//
// 对象类型定义在全局命名空间并被多个租户共享；具体实例归属某个租户命名空间。
// 权限规则分为全局默认规则与租户覆盖规则两层，访问判定以“实例归属租户”的
// 覆盖规则为准（归属方向：实例 -> 租户，而不是主体 -> 租户）。
package ontology

// Effect 是规则条目的效果：允许或拒绝。
type Effect int

const (
	Deny Effect = iota
	Allow
)

func (e Effect) String() string {
	if e == Allow {
		return "allow"
	}
	return "deny"
}

// Action 是被判定或被规则约束的操作，例如 "read"、"write"。
type Action string

// Subject 是发起访问的主体。主体本身不需要在系统中注册，
// 判定只依赖其携带的角色与归属租户等属性。
type Subject struct {
	ID     string
	Tenant string
	Roles  []string
}

// Instance 是对象类型的一个具体实例，归属某个租户命名空间。
type Instance struct {
	ID          string
	Type        string
	OwnerTenant string
	Attributes  map[string]string
}

// PredicateFunc 是行级谓词：给定实例与主体，返回谓词是否成立。
type PredicateFunc func(Instance, Subject) bool

// ObjectTypeDef 是全局命名空间中的对象类型定义。
type ObjectTypeDef struct {
	Name       string
	Attributes []string
	// Predicates 是该类型可用的行级谓词表，规则条目按名字引用。
	Predicates map[string]PredicateFunc
	// RequireRelaxationBasis 声明该类型的“放宽”覆盖是否必须附带授权依据。
	// 系统通过该字段显式声明放宽是否需要额外授权依据。
	RequireRelaxationBasis bool
}

// RuleEntry 是一条规则条目，可同时出现在全局默认规则集与租户覆盖规则集中。
//
// Role、Attribute、Predicate 为空字符串表示该维度为通配（不限制）；
// Action 必须非空且精确匹配。
type RuleEntry struct {
	Role      string
	Action    Action
	Attribute string
	Predicate string
	Effect    Effect
	// Basis 是放宽覆盖所需的授权依据（例如审批单号）。
	// 仅当条目构成放宽且类型声明 RequireRelaxationBasis 时强制要求非空。
	Basis string
}

// Layer 标记一条生效条目来自哪个规则层级。
type Layer int

const (
	LayerDefault Layer = iota
	LayerOverride
)

func (l Layer) String() string {
	if l == LayerOverride {
		return "override"
	}
	return "default"
}

// WinningEntry 是判定依据中胜出的一条规则条目及其来源层级。
type WinningEntry struct {
	Layer Layer
	Entry RuleEntry
}

// DecisionBasis 记录一次判定所依据的规则层级信息，用于审计与开销证明。
type DecisionBasis struct {
	// OwnerTenant 是判定实际采用的覆盖规则所属租户（实例归属租户）。
	OwnerTenant string
	// DefaultVersion / OverrideVersion 是判定时读取的规则快照版本。
	DefaultVersion  uint64
	OverrideVersion uint64 // 0 表示该租户未登记覆盖规则
	// WinningEntries 是裁决该次判定的最高具体度生效条目。
	WinningEntries []WinningEntry
	// OverridesExamined / DefaultsExamined 是本次判定考察的规则条目数，
	// 是可观测的开销证明：只与实例归属租户登记的覆盖条目数相关。
	OverridesExamined int
	DefaultsExamined  int
	// DefaultsSubsumed 是被覆盖规则完全替代的默认条目数。
	DefaultsSubsumed int
}

// Decision 是一次访问判定的结果。
type Decision struct {
	Effect Effect
	Basis  DecisionBasis
}
