package narledger

import "fmt"

// ErrorCode 对错误进行可区分的分类，数值顺序即报错优先级。
type ErrorCode int

const (
	ErrInvalidParam  ErrorCode = iota + 1 // 参数非法
	ErrClockRollback                      // 时钟回退
	ErrReviewer                           // 复核人人数不足/同一人/为申请人本人
	ErrUnauthorized                       // 复核人授权无效
	ErrNotFound                           // 对象不存在
	ErrState                              // 状态不符
	ErrDeptLocked                         // 科室被锁定
	ErrOpenLimit                          // 超过未结清单据上限
	ErrStock                              // 库存不足
	ErrExceed                             // 数量超出
)

func (c ErrorCode) String() string { return codeName[c] }

var codeName = map[ErrorCode]string{
	ErrInvalidParam:  "INVALID_PARAM",
	ErrClockRollback: "CLOCK_ROLLBACK",
	ErrReviewer:      "REVIEWER_INVALID",
	ErrUnauthorized:  "UNAUTHORIZED",
	ErrNotFound:      "NOT_FOUND",
	ErrState:         "STATE_CONFLICT",
	ErrDeptLocked:    "DEPARTMENT_LOCKED",
	ErrOpenLimit:     "OPEN_ORDER_LIMIT",
	ErrStock:         "INSUFFICIENT_STOCK",
	ErrExceed:        "QUANTITY_EXCEEDED",
}

// OpError 携带错误类别与判定依据，便于日志复现。
type OpError struct {
	Code   ErrorCode
	Op     string
	Reason string
}

func (e *OpError) Error() string {
	return fmt.Sprintf("%s: op=%s: %s", e.Code, e.Op, e.Reason)
}

func opError(op string, code ErrorCode, format string, args ...any) *OpError {
	return &OpError{Code: code, Op: op, Reason: fmt.Sprintf(format, args...)}
}
