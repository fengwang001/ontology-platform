package history

import (
	"errors"
	"fmt"
)

// ErrKind 区分五类可识别的错误。
type ErrKind int

const (
	KindInvalidArgument  ErrKind = iota // 参数非法：位移越界、空地址、负上限等
	KindClockRollback                   // 时钟回退
	KindSuperseded                      // 遍历请求被更晚的请求取代
	KindDocumentNotFound                // 文档标识不存在
	KindInvalidState                    // 状态不允许：如对己卸载文档做缓存期操作
)

func (k ErrKind) String() string {
	switch k {
	case KindInvalidArgument:
		return "invalid-argument"
	case KindClockRollback:
		return "clock-rollback"
	case KindSuperseded:
		return "superseded"
	case KindDocumentNotFound:
		return "document-not-found"
	case KindInvalidState:
		return "invalid-state"
	}
	return "unknown"
}

// Error 是内核返回的唯一错误类型，Kind 字段可区分错误类别。
type Error struct {
	Kind ErrKind
	Msg  string
}

func (e *Error) Error() string { return e.Kind.String() + ": " + e.Msg }

// KindOf 提取错误的类别；非内核错误返回 false。
func KindOf(err error) (ErrKind, bool) {
	var he *Error
	if errors.As(err, &he) {
		return he.Kind, true
	}
	return 0, false
}

func errf(kind ErrKind, format string, args ...any) *Error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}
