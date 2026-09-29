package di

import "errors"

var (
	// ErrContainerFrozen 表示容器已冻结，不能再注册。
	ErrContainerFrozen = errors.New("di: container is frozen")
	// ErrContainerClosed 表示容器已关闭。
	ErrContainerClosed = errors.New("di: container is closed")
	// ErrScopeClosed 表示作用域已关闭。
	ErrScopeClosed = errors.New("di: scope is closed")
	// ErrNotFrozen 表示容器尚未冻结，不能解析或创建作用域。
	ErrNotFrozen = errors.New("di: container is not frozen")
	// ErrScopedFromRoot 表示从根作用域解析作用域服务。
	ErrScopedFromRoot = errors.New("di: cannot resolve scoped service from root scope")
)

// errRolledBack 用于暂存作用域实例随领导者解析失败时通知同批等待者。
var errRolledBack = errors.New("di: scoped service discarded because the leading resolution failed")

// RegistrationError 报告注册阶段发现的问题（重复注册、依赖缺失、成环、俘获）。
type RegistrationError struct {
	Kind   string
	Detail string
}

func (e *RegistrationError) Error() string {
	return "di: " + e.Kind + ": " + e.Detail
}
