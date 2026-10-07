package temporalauth

import "errors"

// ErrorCode 是对外暴露的稳定错误分类标识。错误响应只携带代码与请求 ID，
// 不携带任何与属性取值、归一化时区或窗口边界有关的细节。
type ErrorCode string

const (
	// ErrRegionTimezoneUnresolved 对象所属地区的默认时区定义在归一化时无法确定。
	ErrRegionTimezoneUnresolved ErrorCode = "REGION_TIMEZONE_UNRESOLVED"
	// ErrWindowRuleInvalid 权限判定所依赖的时间窗口规则校验失败。
	ErrWindowRuleInvalid ErrorCode = "WINDOW_RULE_INVALID"
	// ErrViewerMissing 查询者身份信息缺失导致无法确定查询者所在时区。
	ErrViewerMissing ErrorCode = "VIEWER_MISSING"
	// ErrPropertyDeprecated 属性因对象类型版本迁移在当前版本已被废弃。
	ErrPropertyDeprecated ErrorCode = "PROPERTY_DEPRECATED"
	// ErrDenied 权限判定为拒绝（不泄露原因细节）。
	ErrDenied ErrorCode = "DENIED"
)

// 错误报告优先级（数值越小优先级越高）。当同一次查看请求同时具备多类错误
// 条件时，系统只报告优先级最高的那一类，保证外部观察到的错误类型确定且
// 不依赖内部求值顺序。
//
// 1. 归一化基准本身无法确定（任何后续判定都失去前提）；
// 2. 窗口规则校验失败（基准可确定后，规则必须先可用）；
// 3. 查询者身份缺失（需要查询者时区参与输入解读时）；
// 4. 属性已废弃（对象类型版本迁移导致属性不可见）。
var errorPriority = map[ErrorCode]int{
	ErrRegionTimezoneUnresolved: 1,
	ErrWindowRuleInvalid:        2,
	ErrViewerMissing:            3,
	ErrPropertyDeprecated:       4,
	ErrDenied:                   5,
}

// HigherPriority reports whether a has higher report priority than b.
func HigherPriority(a, b ErrorCode) bool {
	return errorPriority[a] < errorPriority[b]
}

// AuthError 是判定阶段产生的可分类错误。
type AuthError struct {
	Code ErrorCode
}

func (e *AuthError) Error() string { return string(e.Code) }

// AsAuthError 尝试把错误转换为 *AuthError。
func AsAuthError(err error) (*AuthError, bool) {
	var target *AuthError
	if errors.As(err, &target) {
		return target, true
	}
	return nil, false
}
