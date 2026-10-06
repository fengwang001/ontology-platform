package cancel

import "fmt"

// ErrCode 是可程序化区分的错误类别。
type ErrCode int

const (
	ErrInvalidParam   ErrCode = iota + 1 // 参数非法
	ErrClockRollback                     // 时钟回退
	ErrOrderNotFound                     // 订单不存在
	ErrStageOrder                        // 阶段次序错误
	ErrDelivered                         // 已送达
	ErrCancelled                         // 已取消（终态）
	ErrCancelPending                     // 取消待决
	ErrNotCancellable                    // 不可取消（取货后未迟到）
	ErrPickedUp                          // 已取货（骑手取消）
	ErrForbidden                         // 无权（发起方与阶段不匹配）
	ErrClaimTimeout                      // 声明超时（窗口外声明）
	ErrNoDispute                         // 无争议可声明
)

// Error 携带错误码，调用方可通过 errors.As / Code() 区分。
type Error struct {
	Code ErrCode
	Msg  string
}

func (e *Error) Error() string { return fmt.Sprintf("cancel: %s: %s", e.Code.Name(), e.Msg) }

// Code 从错误中提取错误码；非本系统错误返回 0。
func Code(err error) ErrCode {
	if err == nil {
		return 0
	}
	if e, ok := err.(*Error); ok {
		return e.Code
	}
	return 0
}

func codeErr(code ErrCode, format string, args ...any) error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// Name 返回错误码的稳定英文标识。
func (c ErrCode) Name() string {
	switch c {
	case ErrInvalidParam:
		return "invalid_param"
	case ErrClockRollback:
		return "clock_rollback"
	case ErrOrderNotFound:
		return "order_not_found"
	case ErrStageOrder:
		return "stage_order"
	case ErrDelivered:
		return "delivered"
	case ErrCancelled:
		return "cancelled"
	case ErrCancelPending:
		return "cancel_pending"
	case ErrNotCancellable:
		return "not_cancellable"
	case ErrPickedUp:
		return "picked_up"
	case ErrForbidden:
		return "forbidden"
	case ErrClaimTimeout:
		return "claim_timeout"
	case ErrNoDispute:
		return "no_dispute"
	default:
		return "unknown"
	}
}
