package revision

// ErrorCode 标识修订表达式解析过程中可区分的裁决类型。
type ErrorCode int

const (
	ErrInvalid         ErrorCode = iota // 参数/文法非法
	ErrSymbolicCycle                    // 符号引用成环（仅设置时）
	ErrRefIDAmbiguous                   // 引用与标识歧义
	ErrPrefixAmbiguous                  // 前缀歧义
	ErrNotFound                         // 不存在
	ErrReflogOnID                       // 非引用不可用日志
	ErrReflogMissing                    // 记录不存在
	ErrNotNavigable                     // 类型不可导航
	ErrParentMissing                    // 父不存在
	ErrBeyondHistory                    // 超出历史
	ErrTypeMismatch                     // 类型不符
)

// ResolutionError 携带错误码与裁决所需的附加上下文（候选个数、失败段下标）。
type ResolutionError struct {
	Code       ErrorCode
	Candidates int // ErrPrefixAmbiguous 时的候选对象数
	SegIndex   int // 首个失败导航段的下标（-1 表示首段）
	Detail     string
}

func (e *ResolutionError) Error() string { return e.Detail }

// IsCode 判断 err 是否为指定裁决码。
func IsCode(err error, code ErrorCode) bool {
	re, ok := err.(*ResolutionError)
	return ok && re.Code == code
}
