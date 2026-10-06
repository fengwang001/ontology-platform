package quota

import "fmt"

// Kind 是可区分的错误类别，按优先级从高到低排列（数值小者优先级高）。
// 同一调用存在多类问题时，只返回优先级最高的一类。
type Kind int

const (
	// KindInvalidArgument 参数非法：负数数量、空名称、未知作用域字面量、
	// 硬上限资源名形式非法、操作不存在的 Pod / 配额等。
	KindInvalidArgument Kind = iota
	// KindNamespaceNotFound 命名空间不存在。
	KindNamespaceNotFound
	// KindAlreadyExists 重复创建（命名空间 / Pod / 配额重名）。
	KindAlreadyExists
	// KindInvalidConfiguration 非法配置：矛盾作用域、含尽力型作用域的
	// 配额限制了 pods 以外的资源。
	KindInvalidConfiguration
	// KindMissingDeclaration 缺失声明：配额限制了 requests.X / limits.X，
	// 而 Pod 补全后该资源仍缺省。
	KindMissingDeclaration
	// KindQuotaExceeded 额度超限：已用量 + 本 Pod 量超过硬上限。
	KindQuotaExceeded
)

func (k Kind) String() string {
	switch k {
	case KindInvalidArgument:
		return "InvalidArgument"
	case KindNamespaceNotFound:
		return "NamespaceNotFound"
	case KindAlreadyExists:
		return "AlreadyExists"
	case KindInvalidConfiguration:
		return "InvalidConfiguration"
	case KindMissingDeclaration:
		return "MissingDeclaration"
	case KindQuotaExceeded:
		return "QuotaExceeded"
	}
	return "Unknown"
}

// Error 是控制器返回的错误，携带类别与定位信息。
// 准入失败时 Quota / Resource 定位到配额名称升序、其下资源名称升序的第一处。
type Error struct {
	Kind     Kind
	Message  string
	Quota    string
	Resource ResourceName
}

func (e *Error) Error() string {
	loc := ""
	if e.Quota != "" {
		loc = fmt.Sprintf(" [quota=%s resource=%s]", e.Quota, e.Resource)
	}
	return fmt.Sprintf("%s: %s%s", e.Kind, e.Message, loc)
}

// IsKind 报告 err 是否为指定类别的错误。
func IsKind(err error, k Kind) bool {
	e, ok := err.(*Error)
	return ok && e.Kind == k
}

func invalidArg(msg string) *Error { return &Error{Kind: KindInvalidArgument, Message: msg} }

func nsNotFound(ns string) *Error {
	return &Error{Kind: KindNamespaceNotFound, Message: fmt.Sprintf("命名空间 %q 不存在", ns)}
}

func alreadyExists(what, name string) *Error {
	return &Error{Kind: KindAlreadyExists, Message: fmt.Sprintf("%s %q 已存在", what, name)}
}

func invalidConfig(msg string) *Error {
	return &Error{Kind: KindInvalidConfiguration, Message: msg}
}

func missingDeclaration(quota string, r ResourceName) *Error {
	return &Error{
		Kind:     KindMissingDeclaration,
		Message:  fmt.Sprintf("配额 %q 限制了 %s，但 pod 补全后仍缺省", quota, r),
		Quota:    quota,
		Resource: r,
	}
}

func quotaExceeded(quota string, r ResourceName, used, delta, hard int64) *Error {
	return &Error{
		Kind: KindQuotaExceeded,
		Message: fmt.Sprintf("配额 %q 资源 %s: 已用 %d + 本次 %d = %d 超过硬上限 %d",
			quota, r, used, delta, used+delta, hard),
		Quota:    quota,
		Resource: r,
	}
}
