package settlement

import "fmt"

// ErrorKind 区分被拒绝操作的错误类别。
// 当一个操作同时违反多条规则时，按以下声明顺序只报告第一个类别：
// 参数非法 < 时钟回退 < 不存在 < 状态不允许 < 重复结算 < 变更超出合同总额。
type ErrorKind int

const (
	// ErrInvalidParam 参数非法（空 ID、负数金额、比例越界等）。
	ErrInvalidParam ErrorKind = iota
	// ErrClockRollback 时钟回退：now 小于上一次被接受操作的 now。
	ErrClockRollback
	// ErrNotFound 合同或里程碑（或缺陷）不存在。
	ErrNotFound
	// ErrStateNotAllowed 当前状态不允许该操作（合同已终止、质保期未满等）。
	ErrStateNotAllowed
	// ErrDuplicateSettlement 里程碑已验收通过并结算，重复结算。
	ErrDuplicateSettlement
	// ErrChangeExceedsTotal 变更后各里程碑应付之和超过合同总金额。
	ErrChangeExceedsTotal
)

func (k ErrorKind) String() string {
	switch k {
	case ErrInvalidParam:
		return "invalid_param"
	case ErrClockRollback:
		return "clock_rollback"
	case ErrNotFound:
		return "not_found"
	case ErrStateNotAllowed:
		return "state_not_allowed"
	case ErrDuplicateSettlement:
		return "duplicate_settlement"
	case ErrChangeExceedsTotal:
		return "change_exceeds_total"
	default:
		return "unknown"
	}
}

// Error 是服务返回的唯一错误类型，Kind 可供调用方区分类别。
type Error struct {
	Kind ErrorKind
	Op   string
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s: %s", e.Op, e.Kind, e.Msg)
}

func newError(kind ErrorKind, op, format string, args ...any) *Error {
	return &Error{Kind: kind, Op: op, Msg: fmt.Sprintf(format, args...)}
}
