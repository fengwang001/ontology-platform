package surge

import (
	"errors"
	"fmt"
)

// ErrorCode 可程序化区分的错误类别。
type ErrorCode string

const (
	ErrInvalidConfig       ErrorCode = "invalid_config"   // 构造参数非法
	ErrInvalidArgument     ErrorCode = "invalid_argument" // 操作参数非法
	ErrClockRollback       ErrorCode = "clock_rollback"   // 时钟回退
	ErrRegionNotFound      ErrorCode = "region_not_found" // 区域不存在
	ErrRiderNotFound       ErrorCode = "rider_not_found"  // 骑手不存在
	ErrOrderNotFound       ErrorCode = "order_not_found"  // 订单不存在
	ErrRiderAlreadyOnline  ErrorCode = "rider_already_online"
	ErrRiderAlreadyOffline ErrorCode = "rider_already_offline"
	ErrMoveNotNeeded       ErrorCode = "move_not_needed"       // 骑手已在目标区域
	ErrOrderDispatched     ErrorCode = "order_dispatched"      // 订单已派出
	ErrOrderCompleted      ErrorCode = "order_completed"       // 订单已完成
	ErrOrderCancelled      ErrorCode = "order_cancelled"       // 订单已取消
	ErrOrderNotDispatched  ErrorCode = "order_not_dispatched"  // 完成的订单尚未派出
	ErrRiderOffline        ErrorCode = "rider_offline"         // 派单：骑手不在线
	ErrRiderWrongRegion    ErrorCode = "rider_wrong_region"    // 派单：骑手不在本区
	ErrRiderHoldFull       ErrorCode = "rider_hold_full"       // 派单：骑手持单已满
	ErrEvaluateTooFrequent ErrorCode = "evaluate_too_frequent" // 评估过频
)

// Error 带类型码的错误，可用 errors.As 取出 Code 程序化区分。
type Error struct {
	Code ErrorCode
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Msg)
}

func errf(code ErrorCode, format string, args ...any) error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// CodeOf 提取错误码；nil 返回空串；非本包错误返回 ErrorCode("unknown")。
func CodeOf(err error) ErrorCode {
	if err == nil {
		return ""
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return "unknown"
}
