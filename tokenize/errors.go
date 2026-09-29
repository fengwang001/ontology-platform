package tokenize

import (
	"errors"
	"fmt"
)

// ErrorKind 标识脱敏失败的互斥错误类别。
type ErrorKind int

const (
	// KindInvalidConfig 配置非法（域/表/列定义矛盾）。
	KindInvalidConfig ErrorKind = iota + 1
	// KindUnknownTable 事件引用了未配置的表。
	KindUnknownTable
	// KindInvalidEvent 事件形状非法。
	KindInvalidEvent
	// KindTokenLimitExceeded 某域令牌表达到上限。
	KindTokenLimitExceeded
)

// String 返回错误类别的可读名称。
func (k ErrorKind) String() string {
	switch k {
	case KindInvalidConfig:
		return "invalid_config"
	case KindUnknownTable:
		return "unknown_table"
	case KindInvalidEvent:
		return "invalid_event"
	case KindTokenLimitExceeded:
		return "token_limit_exceeded"
	default:
		return "unknown"
	}
}

// Error 携带可区分的错误类别与具体原因。
type Error struct {
	Kind   ErrorKind
	Reason string
}

func (e *Error) Error() string {
	return fmt.Sprintf("tokenize: %s: %s", e.Kind, e.Reason)
}

func reject(kind ErrorKind, format string, args ...any) error {
	return &Error{Kind: kind, Reason: fmt.Sprintf(format, args...)}
}

// KindOf 从错误中提取错误类别；非本包错误返回 0。
func KindOf(err error) ErrorKind {
	var te *Error
	if errors.As(err, &te) {
		return te.Kind
	}
	return 0
}
