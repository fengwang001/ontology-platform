package ncd

import "fmt"

// Code 为可区分的错误类别。拒绝次序固定为声明顺序：
// 参数非法 > 被保人不存在 > 时钟回退 > 已有在保保单 > 事故已存在 >
// 事故不存在 > 事故日未承保 > 等级不足 > 已购买 > 续保窗口外。
type Code int

const (
	ErrInvalidParam Code = iota + 1
	ErrInsuredNotFound
	ErrClockRegression
	ErrActivePolicyExists
	ErrClaimExists
	ErrClaimNotFound
	ErrAccidentNotCovered
	ErrLevelTooLow
	ErrProtectionBought
	ErrOutOfRenewalWindow
)

var codeNames = map[Code]string{
	ErrInvalidParam:       "参数非法",
	ErrInsuredNotFound:    "被保人不存在",
	ErrClockRegression:    "时钟回退",
	ErrActivePolicyExists: "已有在保保单",
	ErrClaimExists:        "事故已存在",
	ErrClaimNotFound:      "事故不存在",
	ErrAccidentNotCovered: "事故日未承保",
	ErrLevelTooLow:        "等级不足",
	ErrProtectionBought:   "已购买",
	ErrOutOfRenewalWindow: "续保窗口外",
}

func (c Code) String() string {
	if s, ok := codeNames[c]; ok {
		return s
	}
	return "未知错误"
}

// Error 为引擎返回的业务错误，携带类别、入口与说明。
type Error struct {
	Code Code
	Op   string
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("ncd.%s: %s: %s", e.Op, e.Code, e.Msg)
}

// HasCode 报告 err 是否为类别 c 的业务错误。
func HasCode(err error, c Code) bool {
	ne, ok := err.(*Error)
	return ok && ne.Code == c
}

func newErr(op string, c Code, format string, args ...any) *Error {
	return &Error{Code: c, Op: op, Msg: fmt.Sprintf(format, args...)}
}
