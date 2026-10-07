package billing

import "fmt"

// ErrCode 是服务对外报告的固定错误类别，报告次序严格固定。
type ErrCode int

const (
	ErrInvalidArgument      ErrCode = iota + 1 // 参数非法
	ErrClockRollback                           // 时钟回退
	ErrMeterNotFound                           // 表不存在
	ErrReadingOutOfOrder                       // 读数乱序
	ErrReadingIllegal                          // 读数非法（翻转差额超量程一半）
	ErrEstimateNotAllowed                      // 估抄条件不满足
	ErrPeriodAlreadySettled                    // 账期已结算不可重复结算
	ErrSharedNegative                          // 公摊为负
)

// Error 携带固定错误码与人类可读说明。
type Error struct {
	Code ErrCode
	Msg  string
}

func (e *Error) Error() string { return codeName[e.Code] + ": " + e.Msg }

var codeName = map[ErrCode]string{
	ErrInvalidArgument:      "invalid argument",
	ErrClockRollback:        "clock rollback",
	ErrMeterNotFound:        "meter not found",
	ErrReadingOutOfOrder:    "reading out of order",
	ErrReadingIllegal:       "reading illegal",
	ErrEstimateNotAllowed:   "estimate not allowed",
	ErrPeriodAlreadySettled: "period already settled",
	ErrSharedNegative:       "shared usage negative",
}

func errf(code ErrCode, format string, args ...any) error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}
