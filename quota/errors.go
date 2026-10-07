package quota

import (
	"errors"
	"fmt"
)

// ErrorKind 是错误类别。声明顺序即优先级（从高到低）：
// 参数非法 > 命名空间不存在 > 重复创建 > 非法配置 > 缺失声明 > 额度超限。
type ErrorKind int

const (
	// ErrInvalidArgument 参数非法：空名称、负数量、负期限、引用不存在的 Pod/配额等。
	ErrInvalidArgument ErrorKind = iota
	// ErrNamespaceNotFound 命名空间不存在。
	ErrNamespaceNotFound
	// ErrAlreadyExists 重复创建：命名空间、Pod 或配额同名。
	ErrAlreadyExists
	// ErrInvalidConfiguration 非法配置：矛盾作用域、未知作用域、
	// 硬上限资源名形式非法、尽力型配额限制非 pods 资源。
	ErrInvalidConfiguration
	// ErrMissingDeclaration 缺失声明：配额限制的 requests.X/limits.X 对该 Pod 缺省。
	ErrMissingDeclaration
	// ErrQuotaExceeded 额度超限：已用量加本 Pod 的量超过硬上限。
	ErrQuotaExceeded
)

func (k ErrorKind) String() string {
	switch k {
	case ErrInvalidArgument:
		return "参数非法"
	case ErrNamespaceNotFound:
		return "命名空间不存在"
	case ErrAlreadyExists:
		return "重复创建"
	case ErrInvalidConfiguration:
		return "非法配置"
	case ErrMissingDeclaration:
		return "缺失声明"
	case ErrQuotaExceeded:
		return "额度超限"
	default:
		return fmt.Sprintf("未知错误类别(%d)", int(k))
	}
}

// Priority 返回错误类别优先级，数值越小优先级越高。
func (k ErrorKind) Priority() int { return int(k) }

// Error 是控制器返回的结构化错误。
type Error struct {
	Kind ErrorKind
	// Quota / Resource 仅在准入类错误（缺失声明、额度超限）中有意义。
	Quota    string
	Resource ResourceName
	Msg      string
}

func (e *Error) Error() string {
	s := e.Kind.String()
	if e.Quota != "" {
		s += " quota=" + e.Quota
	}
	if e.Resource != "" {
		s += " resource=" + string(e.Resource)
	}
	if e.Msg != "" {
		s += ": " + e.Msg
	}
	return s
}

// KindOf 取出 err 的错误类别；err 不是 *Error 时返回 false。
func KindOf(err error) (ErrorKind, bool) {
	var qe *Error
	if errors.As(err, &qe) {
		return qe.Kind, true
	}
	return 0, false
}

// IsKind 判断 err 是否属于给定类别。
func IsKind(err error, kind ErrorKind) bool {
	k, ok := KindOf(err)
	return ok && k == kind
}
