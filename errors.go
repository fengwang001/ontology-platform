package ontology

import "fmt"

// ErrorClass 标识系统汇报的错误类别。
//
// 系统按固定且唯一的优先顺序汇报错误，优先级从高到低为：
//
//	ErrUnknownAttribute  判定规则引用了不存在的属性（或请求了未声明的属性）
//	ErrUnknownTag        判定规则引用了未注册（或对象类型不匹配）的标签
//	ErrCyclicRule        判定规则之间构成循环依赖
//	ErrSnapshotUnavailable 可重复读声明的快照已过期或不存在
//	ErrUnknownObjectType 对象实例所属的对象类型未注册
//	ErrRuleType          判定规则求值时遇到类型不匹配（如布尔上下文出现非布尔值）
//	ErrPermissionDenied  权限合并结论为拒绝
//	ErrTagInUse          试图删除仍被其他规则引用的标签规则
//
// 当一次调用同时满足多个错误条件时，必须且只能汇报优先级最高的那一类。
type ErrorClass int

const (
	ErrUnknownAttribute ErrorClass = iota + 1
	ErrUnknownTag
	ErrCyclicRule
	ErrSnapshotUnavailable
	ErrUnknownObjectType
	ErrRuleType
	ErrPermissionDenied
	ErrTagInUse
)

func (c ErrorClass) String() string {
	switch c {
	case ErrUnknownAttribute:
		return "unknown attribute"
	case ErrUnknownTag:
		return "unknown tag"
	case ErrCyclicRule:
		return "cyclic rule dependency"
	case ErrSnapshotUnavailable:
		return "snapshot unavailable"
	case ErrUnknownObjectType:
		return "unknown object type"
	case ErrRuleType:
		return "rule type error"
	case ErrPermissionDenied:
		return "permission denied"
	case ErrTagInUse:
		return "tag in use"
	}
	return "unknown error class"
}

// Error 是系统所有可预期失败的统一错误类型。
// Message 中绝不包含任何属性取值，以避免向无权主体泄露信息。
type Error struct {
	Class   ErrorClass
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Class, e.Message)
}

// IsClass 报告 err 是否属于给定错误类别。
func IsClass(err error, class ErrorClass) bool {
	if e, ok := err.(*Error); ok {
		return e.Class == class
	}
	return false
}

func errf(class ErrorClass, format string, args ...any) *Error {
	return &Error{Class: class, Message: fmt.Sprintf(format, args...)}
}

// NewErrorf 构造一个指定类别的错误，供外部参照实现等复用同一错误体系。
func NewErrorf(class ErrorClass, format string, args ...any) *Error {
	return errf(class, format, args...)
}
