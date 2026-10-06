package genericinst

// ErrorCode 是请求被拒绝时的稳定错误码，拒绝次序由 ErrorOrder 固定。
type ErrorCode int

const (
	ErrUndefined  ErrorCode = iota + 1 // 未定义错误
	ErrArgument                        // 参数错误（个数不符 / 缺少必填实参）
	ErrConstraint                      // 约束错误
	ErrDepth                           // 嵌套深度错误
	ErrQuota                           // 配额错误
	ErrDependency                      // 清理依赖错误
)

// ErrorOrder 规定校验时的固定拒绝次序：
// 未定义 > 参数错误 > 约束错误 > 深度错误 > 配额错误。
var ErrorOrder = []ErrorCode{ErrUndefined, ErrArgument, ErrConstraint, ErrDepth, ErrQuota, ErrDependency}

func (c ErrorCode) String() string {
	switch c {
	case ErrUndefined:
		return "undefined"
	case ErrArgument:
		return "argument"
	case ErrConstraint:
		return "constraint"
	case ErrDepth:
		return "depth"
	case ErrQuota:
		return "quota"
	case ErrDependency:
		return "dependency"
	default:
		return "unknown"
	}
}

// RequestError 携带错误码与人类可读原因。
type RequestError struct {
	Code ErrorCode
	Msg  string
}

func (e *RequestError) Error() string { return e.Code.String() + ": " + e.Msg }
