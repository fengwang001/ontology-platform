package lifecycle

// State 是对象类型状态机中的一个状态名。
type State string

// LinkType 标识一种（有向）链接类型。
type LinkType string

// AttrKey 是实例属性名。
type AttrKey string

// AttrValue 是可比较的属性值（int64/string/bool 等）。
type AttrValue any

// InstanceID 唯一标识一个对象实例。
type InstanceID string

// Comparator 是属性前置条件使用的比较运算符。
type Comparator string

const (
	CmpEq Comparator = "eq"
	CmpNe Comparator = "ne"
	CmpLt Comparator = "lt"
	CmpLe Comparator = "le"
	CmpGt Comparator = "gt"
	CmpGe Comparator = "ge"
)

// AttrCheck 引用触发实例当前的属性取值。
type AttrCheck struct {
	Key   AttrKey
	Op    Comparator
	Value AttrValue
}

// LinkCountCheck 引用触发实例经由某链接类型（出向）连接到的实例数量。
type LinkCountCheck struct {
	Link LinkType
	Min  int // 闭区间下界；负数表示不约束下界
	Max  int // 闭区间上界；负数表示不约束上界
}

// LinkStateCheck 引用经由某链接类型连接到的邻居实例的状态。
// RequireAll 为 true 时所有现存邻居必须处于 AllowedStates；
// 为 false 时至少存在一个邻居处于 AllowedStates（不存在邻居则不成立）。
type LinkStateCheck struct {
	Link          LinkType
	AllowedStates []State
	RequireAll    bool
}

// Precondition 是迁移前置条件的三选一之和。一次迁移只有在触发时
// 全部前置条件同时成立才被允许。
type Precondition struct {
	Attr      *AttrCheck
	LinkCount *LinkCountCheck
	LinkState *LinkStateCheck
}

// AttrBound 是属性校验钩子：迁移生效后某个属性必须落在
// [Min, Max]（数值）或属于 AllowedValues（非数值）。校验失败归类为
// ErrPrecondition。
type AttrBound struct {
	Key           AttrKey
	Min           *int64
	Max           *int64
	AllowedValues []AttrValue
}

// CardinalityBound 要求迁移生效后，触发实例经由 Link 出向连接的
// 实例数量落在 [Min, Max]。校验使用迁移生效后的真实链接状态。
type CardinalityBound struct {
	Link LinkType
	Min  int
	Max  int // -1 表示无上界
}

// Hook 是链接类型上的跨实例校验钩子：若实例经由 Link 出向连接到
// 邻居，则迁移生效后每个邻居都必须处于 RequireStates 之一。
// 失败归类为 ErrHook，发起迁移的实例也不得生效。
type Hook struct {
	Link          LinkType
	RequireStates []State
}

// Cascade 声明链式触发：本迁移生效后，每个经由 Link 出向连接到的
// 邻居都必须随之执行 ToRule 迁移（邻居类型的规则名）。
type Cascade struct {
	Link   LinkType
	ToRule string
}

// TransitionRule 声明对象类型上的一条有向迁移。
type TransitionRule struct {
	Name          string
	From          []State
	To            State
	Preconditions []Precondition
	AttrBounds    []AttrBound
	Cardinality   []CardinalityBound
	Hooks         []Hook
	Cascades      []Cascade
	// MutexGroup 非空时，同一处理单元内同实例同一组的迁移至多放行一条，
	// 放行哪条由请求上声明的 Priority 决定（数值小者优先）。
	MutexGroup string
}

// ObjectType 声明一个对象类型的状态集合与迁移规则。
type ObjectType struct {
	Name    string
	States  []State
	Initial State
	// TerminalsList 显式声明终态；处于终态的实例不允许再迁移、
	// 不允许改属性、不允许新增链接（允许删除已有链接）。
	TerminalsList []State
	Transitions   map[string]*TransitionRule
}

// IsTerminal 报告某状态是否被声明为终态。
func (t *ObjectType) IsTerminal(s State) bool {
	for _, term := range t.TerminalsList {
		if term == s {
			return true
		}
	}
	return false
}

// AttrOp 是随迁移一并提交的属性变更。
type AttrOp struct {
	Key   AttrKey
	Op    AttrOperator
	Value AttrValue // Set 使用；Add 时要求 int64
}

type AttrOperator string

const (
	AttrSet AttrOperator = "set"
	AttrAdd AttrOperator = "add"
)

// LinkOp 是随迁移一并提交的链接变更（作用于触发实例的出边）。
type LinkOp struct {
	Link   LinkType
	Target InstanceID
	Op     LinkOperator
}

type LinkOperator string

const (
	LinkAdd LinkOperator = "add"
	LinkDel LinkOperator = "del"
)

// TransitionRequest 是一次（可能是并发批次中的一个）迁移触发请求。
// Priority 为调用方声明的互斥优先顺序（数值小者优先）。
type TransitionRequest struct {
	Instance InstanceID
	Rule     string
	Priority int
	Attrs    []AttrOp
	Links    []LinkOp
}
