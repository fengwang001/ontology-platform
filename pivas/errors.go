package pivas

import "fmt"

// Code 标识可区分的错误类别，按声明顺序即上报优先级（先查先报）。
type Code int

const (
	CodeInvalidParam     Code = iota + 1 // 参数非法
	CodeClockRollback                    // 时钟回退
	CodeDrugNotFound                     // 药品不存在
	CodeIncompatiblePair                 // 禁忌配对
	CodeSolventMismatch                  // 溶媒不兼容
	CodeLightConflict                    // 避光冲突
	CodeNoFeasibleSlot                   // 无可行安排
	CodeStateConflict                    // 状态不符
	CodeOrderNotFound                    // 医嘱不存在
)

func (c Code) String() string {
	switch c {
	case CodeInvalidParam:
		return "参数非法"
	case CodeClockRollback:
		return "时钟回退"
	case CodeDrugNotFound:
		return "药品不存在"
	case CodeIncompatiblePair:
		return "禁忌配对"
	case CodeSolventMismatch:
		return "溶媒不兼容"
	case CodeLightConflict:
		return "避光冲突"
	case CodeNoFeasibleSlot:
		return "无可行安排"
	case CodeStateConflict:
		return "状态不符"
	case CodeOrderNotFound:
		return "医嘱不存在"
	}
	return "未知错误"
}

// Error 是系统返回的唯一错误类型，Code 可机器判定，Msg 供人阅读。
type Error struct {
	Code Code
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Msg)
}

func errf(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}
