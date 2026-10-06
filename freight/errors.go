package freight

import "fmt"

// ErrKind 错误类别，用于区分拒绝原因。
type ErrKind int

const (
	ErrInvalidParam   ErrKind = iota // 参数非法
	ErrOverlap                       // 合同生效区间重叠
	ErrNoContract                    // 线路与等级没有任何合同
	ErrTimeNotCovered                // 有合同但揽收时刻不在任何生效区间内
	ErrOutOfRange                    // 超出承运范围
	ErrNotPriced                     // 运单尚未计价
	ErrAlreadySettled                // 运单已结算
)

func (k ErrKind) String() string {
	switch k {
	case ErrInvalidParam:
		return "参数非法"
	case ErrOverlap:
		return "区间重叠"
	case ErrNoContract:
		return "无合同"
	case ErrTimeNotCovered:
		return "时刻未覆盖"
	case ErrOutOfRange:
		return "超出承运范围"
	case ErrNotPriced:
		return "运单未计价"
	case ErrAlreadySettled:
		return "已结算"
	}
	return "未知错误"
}

// Error 带类别的错误。
type Error struct {
	Kind    ErrKind
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Kind, e.Message)
}

func newError(kind ErrKind, format string, args ...any) *Error {
	return &Error{Kind: kind, Message: fmt.Sprintf(format, args...)}
}
