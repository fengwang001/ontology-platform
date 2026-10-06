package signal

import "fmt"

// ErrKind 错误类别，数值顺序即上报优先次序：只报次序最靠前的一类。
type ErrKind int

const (
	ErrInvalidParam     ErrKind = iota + 1 // 参数非法
	ErrClockRollback                       // 时钟回退
	ErrInfeasiblePlan                      // 方案不可行
	ErrPhaseNotExist                       // 相位不存在
	ErrDuplicateRequest                    // 重复请求
	ErrConsecutiveSkip                     // 会造成相邻循环连续跳相
	ErrTargetOccupied                      // 目标相位正被另一紧急请求占用
)

func (k ErrKind) String() string {
	switch k {
	case ErrInvalidParam:
		return "invalid-param"
	case ErrClockRollback:
		return "clock-rollback"
	case ErrInfeasiblePlan:
		return "infeasible-plan"
	case ErrPhaseNotExist:
		return "phase-not-exist"
	case ErrDuplicateRequest:
		return "duplicate-request"
	case ErrConsecutiveSkip:
		return "consecutive-skip"
	case ErrTargetOccupied:
		return "target-occupied"
	}
	return "unknown"
}

// Error 可区分的错误类型。
type Error struct {
	Kind ErrKind
	Msg  string
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Kind, e.Msg) }

// KindOf 提取错误类别；非本包错误返回 0。
func KindOf(err error) ErrKind {
	if e, ok := err.(*Error); ok {
		return e.Kind
	}
	return 0
}

func errf(kind ErrKind, format string, args ...any) *Error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}
