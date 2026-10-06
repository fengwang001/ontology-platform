// Package pivas 实现医院静脉用药配置中心（PIVAS）的排程与稳定期判定。
package pivas

import "fmt"

// ErrorCode 区分所有错误类别。
type ErrorCode int

const (
	ErrInvalidParam     ErrorCode = iota + 1 // 参数非法
	ErrClockRollback                         // 时钟回退
	ErrDrugNotFound                          // 药品不存在
	ErrIncompatiblePair                      // 禁忌配对
	ErrSolventMismatch                       // 溶媒不兼容
	ErrLightConflict                         // 避光冲突
	ErrNoFeasiblePlan                        // 无可行安排
	ErrBadState                              // 状态不符（如取消已开始医嘱）
)

// Error 携带稳定的错误码，便于调用方精确分支处理。
type Error struct {
	Code ErrorCode
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("pivas: %s: %s", codeName(e.Code), e.Msg)
}

func codeName(c ErrorCode) string {
	switch c {
	case ErrInvalidParam:
		return "invalid param"
	case ErrClockRollback:
		return "clock rollback"
	case ErrDrugNotFound:
		return "drug not found"
	case ErrIncompatiblePair:
		return "incompatible pair"
	case ErrSolventMismatch:
		return "solvent mismatch"
	case ErrLightConflict:
		return "light conflict"
	case ErrNoFeasiblePlan:
		return "no feasible plan"
	case ErrBadState:
		return "bad state"
	default:
		return "unknown"
	}
}

func errf(code ErrorCode, format string, args ...any) error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}
