package imaging

// Code 标识操作结果的可区分错误类别。
// 数值越小优先级越高；同一操作同时满足多个错误时只报告优先级最高者。
type Code int

const (
	// OK 表示操作被接受。
	OK Code = iota
	CodeInvalidParam
	CodeClockRollback
	CodeNotFound
	CodeStateMismatch
	CodeDeviceCategoryMismatch
	CodeImplantIncompatible
	CodeRenalMissingOrExpired
	CodeRenalInsufficient
	CodeDeviceConflict
	CodeQCConflict
	CodeObservationFull
	// 以下三类不参与通用优先级排序：
	// CodeCheckinWindow 仅在签到窗口不符时返回（操作被拒绝）；
	// CodeNotHydrated / CodeNotPremedicated 仅作为签到复核失败的原因出现
	// （此时签到仍被接受，预约转为需改期）。
	CodeCheckinWindow
	CodeNotHydrated
	CodeNotPremedicated
)

func (c Code) String() string {
	switch c {
	case OK:
		return "OK"
	case CodeInvalidParam:
		return "参数非法"
	case CodeClockRollback:
		return "时钟回退"
	case CodeNotFound:
		return "对象不存在"
	case CodeStateMismatch:
		return "状态不符"
	case CodeDeviceCategoryMismatch:
		return "设备类别不符"
	case CodeImplantIncompatible:
		return "植入物不兼容"
	case CodeRenalMissingOrExpired:
		return "结果缺失或过期"
	case CodeRenalInsufficient:
		return "肾功能不足"
	case CodeDeviceConflict:
		return "设备时段冲突"
	case CodeQCConflict:
		return "质控时段冲突"
	case CodeObservationFull:
		return "留观位不足"
	case CodeCheckinWindow:
		return "签到窗口不符"
	case CodeNotHydrated:
		return "未水化"
	case CodeNotPremedicated:
		return "未预处理"
	}
	return "未知"
}

// Error 是一次被拒绝操作的结果，携带错误类别与判定依据说明。
type Error struct {
	Code   Code
	Detail string
}

func (e *Error) Error() string {
	if e.Detail == "" {
		return e.Code.String()
	}
	return e.Code.String() + ": " + e.Detail
}

func errf(code Code, detail string) *Error {
	return &Error{Code: code, Detail: detail}
}
