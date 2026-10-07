package idx

import "errors"

// 四类互不相同的错误。当同一次维护操作同时满足多类错误的触发条件时，
// 按 errPriority 定义的优先级只报告其中一类。
var (
	// ErrDeprecatedNoReplacement 待索引的属性字段已被对象类型定义迁移
	// 废弃，且未指定替代字段。优先级最高。
	ErrDeprecatedNoReplacement = errors.New("idx: indexed property deprecated without replacement")
	// ErrSwitchValidationFailed 索引依据字段切换校验失败（既有数据不
	// 满足新字段作为索引依据的约束），切换已整体回滚。
	ErrSwitchValidationFailed = errors.New("idx: index-basis switch validation failed")
	// ErrPropertyNotDefined 变更事件引用的属性在事件生效时刻的对象
	// 类型定义中尚未定义。
	ErrPropertyNotDefined = errors.New("idx: property not defined at event version")
	// ErrSwitchInProgress 查询发生在索引依据字段切换尚未完成的瞬间。
	ErrSwitchInProgress = errors.New("idx: query during in-progress index-basis switch")
)

// errPriority 返回错误的优先级，数值越小优先级越高。
func errPriority(err error) int {
	switch {
	case errors.Is(err, ErrDeprecatedNoReplacement):
		return 0
	case errors.Is(err, ErrSwitchValidationFailed):
		return 1
	case errors.Is(err, ErrPropertyNotDefined):
		return 2
	case errors.Is(err, ErrSwitchInProgress):
		return 3
	default:
		return 1 << 30
	}
}

// pickError 从同时触发的错误中按优先级选出一个上报。
func pickError(errs []error) error {
	var best error
	for _, err := range errs {
		if err == nil {
			continue
		}
		if best == nil || errPriority(err) < errPriority(best) {
			best = err
		}
	}
	return best
}
