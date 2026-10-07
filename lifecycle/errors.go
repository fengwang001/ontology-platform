package lifecycle

// Code 标识一次被拒绝操作的错误类别。
type Code int

const (
	// CodeUnknown 目标状态或迁移规则未声明（优先级最高）。
	CodeUnknown Code = iota + 1
	// CodePrecondition 前置条件不成立。
	CodePrecondition
	// CodeMutex 互斥迁移被拒绝。
	CodeMutex
	// CodeCardinality 迁移后链接基数校验失败。
	CodeCardinality
	// CodeHook 跨实例钩子联动拒绝。
	CodeHook
	// CodeCycle 链式触发中检测到循环。
	CodeCycle
	// CodeTerminal 终态实例不允许任何改动（优先级最低）。
	CodeTerminal
)

// priority 返回错误类别固定的报告优先级：数值越小优先级越高。
func (c Code) priority() int {
	switch c {
	case CodeUnknown:
		return 0
	case CodePrecondition:
		return 1
	case CodeMutex:
		return 2
	case CodeCardinality:
		return 3
	case CodeHook:
		return 4
	case CodeCycle:
		return 5
	case CodeTerminal:
		return 6
	default:
		return 7
	}
}

// String 返回错误码的可读名称。
func (c Code) String() string {
	switch c {
	case CodeUnknown:
		return "unknown-transition"
	case CodePrecondition:
		return "precondition-failed"
	case CodeMutex:
		return "mutex-rejected"
	case CodeCardinality:
		return "cardinality-violated"
	case CodeHook:
		return "hook-rejected"
	case CodeCycle:
		return "cascade-cycle"
	case CodeTerminal:
		return "terminal-protected"
	default:
		return "unknown-error"
	}
}

// Error 携带一次拒绝的完整判定信息。
type Error struct {
	Code       Code
	InstanceID string
	Transition string
	Detail     string
}

func (e *Error) Error() string {
	return e.Code.String() + ": " + e.Detail
}
