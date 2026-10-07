// Package ontology 实现本体平台的变更流消费与补偿子系统。
//
// 子系统消费本体对象与动作产生的变更流，对每一条表示动作已成功执行的
// 变更事件触发一个关联的补偿型动作。补偿动作满足原子性事务要求，且同一
// 条变更事件引发的补偿恰好执行一次。
package ontology

// EventKind 区分变更事件的语义类型。
type EventKind int

const (
	// KindActionSucceeded 表示一个动作已成功执行。
	KindActionSucceeded EventKind = iota
	// KindActionReverted 表示一个先前已成功执行的动作被撤销。
	KindActionReverted
)

func (k EventKind) String() string {
	switch k {
	case KindActionSucceeded:
		return "ActionSucceeded"
	case KindActionReverted:
		return "ActionReverted"
	default:
		return "Unknown"
	}
}

// ChangeEvent 是变更流中的一条事件。
//
// 事件身份规则（去重的判定基础）：
//   - EventID 标识一次投递。网络重试产生的重复投递携带与首次投递完全
//     相同的 EventID 与 ActionExecutionID。
//   - ActionExecutionID 标识一次真实的动作执行。调用者再次调用同一动作
//     时，即使参数与效果等价，也会产生一个全新的 ActionExecutionID。
//
// 因此：两条事件互为重复 当且仅当 ActionExecutionID 相同且 EventID 相同。
// ActionExecutionID 相同而 EventID 不同属于标识信息自相矛盾，按
// ErrIdentityUndecidable 处理。
type ChangeEvent struct {
	EventID           string
	ActionExecutionID string
	Kind              EventKind
	ActionType        string
	// Payload 携带动作参数。对 KindActionReverted 事件，Payload 中必须
	// 含有 OriginalActionExecutionID 指向被撤销的那次执行。
	Payload map[string]any
}

// SideEffect 是补偿动作对单个对象产生的一项副作用。
type SideEffect struct {
	ObjectID string
	Op       Op
	Field    string
	Value    int
}

// Op 是副作用的操作类型。
type Op string

const (
	// OpSet 将对象字段设置为指定值（幂等）。
	OpSet Op = "set"
	// OpAdd 将对象字段增加指定增量。非天然幂等，依赖 journal 的
	// 幂等键保证恰好施加一次。
	OpAdd Op = "add"
	// OpDelete 删除对象（幂等）。
	OpDelete Op = "delete"
)

// knownOps 是被原子性校验认可的合法操作集合。
var knownOps = map[Op]bool{OpSet: true, OpAdd: true, OpDelete: true}

// CompensationBuilder 根据变更事件构造补偿动作的副作用计划。
// 返回错误时按 ErrAtomicityViolation 处理（计划无法构造）。
type CompensationBuilder func(evt ChangeEvent) ([]SideEffect, error)
