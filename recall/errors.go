// Package recall 实现医院药品批次召回与追溯锁定系统。
//
// 系统划分为三个相互协作的模块：
//   - inventory：批次库存、发放/退药（患者持有量）台账；
//   - recalls：按药品隔离的召回登记表，负责批号区间覆盖与有效等级判定；
//   - System：唯一对外入口，负责参数校验、单调时钟、互斥串行化与模块编排。
package recall

import "fmt"

// ErrorCode 为可区分的业务错误码。其数值顺序就是错误上报的优先级顺序：
// 数值越小优先级越高，一次操作同时违反多条时只报第一个。
type ErrorCode int

const (
	// CodeInvalidParam 参数非法：标识为空、数量越界、等级越界等。
	CodeInvalidParam ErrorCode = iota + 1
	// CodeClockRollback 时钟回退：now 小于上一次被接受操作的 now。
	CodeClockRollback
	// CodeNotFound 对象不存在：批次、召回或患者持有记录不存在。
	CodeNotFound
	// CodeRecallForbidden 召回禁止：当前有效召回等级不允许该操作。
	CodeRecallForbidden
	// CodeConsentRequired 需知情确认：三级召回下发药缺少知情确认。
	CodeConsentRequired
	// CodeInsufficientStock 库存不足：调拨/发放数量超过出发位置库存。
	CodeInsufficientStock
	// CodeReturnExceeded 退药超量：退药超过该患者该批次尚未退回的总量。
	CodeReturnExceeded
	// CodeInvalidState 状态不符：如对三级/已解除召回查询追回清单。
	CodeInvalidState
)

// codeName 为错误码提供稳定的可读名称，便于日志与重放比对。
var codeName = map[ErrorCode]string{
	CodeInvalidParam:      "参数非法",
	CodeClockRollback:     "时钟回退",
	CodeNotFound:          "对象不存在",
	CodeRecallForbidden:   "召回禁止",
	CodeConsentRequired:   "需知情确认",
	CodeInsufficientStock: "库存不足",
	CodeReturnExceeded:    "退药超量",
	CodeInvalidState:      "状态不符",
}

// Name 返回错误码的中文名。
func (c ErrorCode) Name() string { return codeName[c] }

// OpError 是系统对外返回的唯一错误类型。
type OpError struct {
	// Code 为错误码，按优先级定义。
	Code ErrorCode
	// Op 为触发错误的操作名。
	Op string
	// Detail 为不影响判定的人类可读说明。
	Detail string
}

func (e *OpError) Error() string {
	return fmt.Sprintf("%s: %s: %s", e.Op, e.Code.Name(), e.Detail)
}

func opError(code ErrorCode, op, detail string) *OpError {
	return &OpError{Code: code, Op: op, Detail: detail}
}

// AsOpError 从 err 中提取 *OpError。
func AsOpError(err error) (*OpError, bool) {
	if err == nil {
		return nil, false
	}
	e, ok := err.(*OpError)
	return e, ok
}
