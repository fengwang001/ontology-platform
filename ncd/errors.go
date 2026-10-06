package ncd

import (
	"errors"
	"fmt"
)

// Code 错误类别。判定次序固定为声明顺序（数值小者优先报出）：
// 参数非法 > 被保人不存在 > 时钟回退 > 已有在保保单 > 事故已存在 >
// 事故不存在 > 事故日未承保 > 等级不足 > 已购买 > 续保窗口外。
type Code int

const (
	ErrInvalidParam       Code = iota + 1 // 参数非法
	ErrInsuredNotFound                    // 被保人不存在
	ErrClockBackward                      // 时钟回退
	ErrActivePolicyExists                 // 已有在保保单
	ErrClaimExists                        // 事故已存在
	ErrClaimNotFound                      // 事故不存在
	ErrAccidentNotCovered                 // 事故日未承保
	ErrGradeTooLow                        // 等级不足
	ErrProtectionBought                   // 已购买
	ErrOutOfRenewalWindow                 // 续保窗口外
	ErrNoActivePolicy                     // 无在保保单（参数非法类的前置条件）
)

func (c Code) String() string {
	switch c {
	case ErrInvalidParam:
		return "参数非法"
	case ErrInsuredNotFound:
		return "被保人不存在"
	case ErrClockBackward:
		return "时钟回退"
	case ErrActivePolicyExists:
		return "已有在保保单"
	case ErrClaimExists:
		return "事故已存在"
	case ErrClaimNotFound:
		return "事故不存在"
	case ErrAccidentNotCovered:
		return "事故日未承保"
	case ErrGradeTooLow:
		return "等级不足"
	case ErrProtectionBought:
		return "已购买"
	case ErrOutOfRenewalWindow:
		return "续保窗口外"
	case ErrNoActivePolicy:
		return "无在保保单"
	}
	return fmt.Sprintf("未知错误(%d)", int(c))
}

// Error 引擎返回的业务错误，携带可区分的类别码。
type Error struct {
	Code Code
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("ncd: %s: %s", e.Code, e.Msg)
}

// HasCode 判断 err 是否为指定类别的业务错误。
func HasCode(err error, c Code) bool {
	var ne *Error
	if errors.As(err, &ne) {
		return ne.Code == c
	}
	return false
}

// CodeOf 提取错误类别，nil 返回 0。
func CodeOf(err error) Code {
	var ne *Error
	if errors.As(err, &ne) {
		return ne.Code
	}
	return 0
}

func errf(c Code, format string, args ...any) *Error {
	return &Error{Code: c, Msg: fmt.Sprintf(format, args...)}
}
