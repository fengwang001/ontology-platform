package contract

import "fmt"

// Category 区分错误类别。声明顺序即报告优先级：
// 一次操作违反多条规则时，只报告优先级最高（序号最小）的第一个错误。
type Category int

const (
	// None 表示操作被接受。
	None Category = iota
	// InvalidParam 参数非法（如空参与方、授权窗口倒置、负日序号）。
	InvalidParam
	// ClockRollback 时钟回退：now 小于上一次被接受操作的 now。
	ClockRollback
	// NotFound 合同或协议不存在。
	NotFound
	// StateNotAllowed 状态不允许（合同已终止/已届满/未开始、重复签署等）。
	StateNotAllowed
	// AuthExpired 授权失效：签署时刻不在授权有效期 [authFrom, authTo] 内。
	AuthExpired
	// CountersignMissing 缺少法务会签。
	CountersignMissing
	// SignExpired 签署超期：一方签署后超过规定天数对方仍未签署。
	SignExpired
)

func (c Category) String() string {
	switch c {
	case None:
		return "none"
	case InvalidParam:
		return "invalid_param"
	case ClockRollback:
		return "clock_rollback"
	case NotFound:
		return "not_found"
	case StateNotAllowed:
		return "state_not_allowed"
	case AuthExpired:
		return "auth_expired"
	case CountersignMissing:
		return "countersign_missing"
	case SignExpired:
		return "sign_expired"
	}
	return fmt.Sprintf("category(%d)", int(c))
}

// Error 是服务返回的唯一错误类型，携带类别与可读信息。
type Error struct {
	Cat Category
	Msg string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Cat, e.Msg)
}

func errf(cat Category, format string, args ...any) *Error {
	return &Error{Cat: cat, Msg: fmt.Sprintf(format, args...)}
}
