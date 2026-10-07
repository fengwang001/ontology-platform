package ncd

import "fmt"

// Code 区分各类业务错误。声明顺序即拒绝次序（小者优先）：
// 参数非法 > 被保人不存在 > 时钟回退 > 已有在保保单 > 事故已存在 >
// 事故不存在 > 事故日未承保 > 等级不足 > 已购买 > 续保窗口外。
type Code int

const (
	CodeInvalidParam Code = iota + 1
	CodeInsuredNotFound
	CodeClockRollback
	CodeActivePolicyExists
	CodeClaimExists
	CodeClaimNotFound
	CodeAccidentNotCovered
	CodeLevelInsufficient
	CodeAlreadyProtected
	CodeOutsideRenewalWindow
)

func (c Code) String() string {
	switch c {
	case CodeInvalidParam:
		return "参数非法"
	case CodeInsuredNotFound:
		return "被保人不存在"
	case CodeClockRollback:
		return "时钟回退"
	case CodeActivePolicyExists:
		return "已有在保保单"
	case CodeClaimExists:
		return "事故已存在"
	case CodeClaimNotFound:
		return "事故不存在"
	case CodeAccidentNotCovered:
		return "事故日未承保"
	case CodeLevelInsufficient:
		return "等级不足"
	case CodeAlreadyProtected:
		return "已购买"
	case CodeOutsideRenewalWindow:
		return "续保窗口外"
	}
	return "未知错误"
}

// Error 是可区分的业务错误，Code 用于判定，Msg 为人读补充。
type Error struct {
	Op   string
	Code Code
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("ncd: %s 拒绝[%s]: %s", e.Op, e.Code, e.Msg)
}

func fail(op string, code Code, msg string) *Error {
	return &Error{Op: op, Code: code, Msg: msg}
}
