package inventory

import (
	"errors"
	"fmt"
)

// ErrKind 被拒绝操作的类别。声明顺序即规范定义的拒绝优先级：
// 参数非法 > 时钟回退 > 条目或航段不存在 > 预占已过期 > 状态不符 > 库存不足。
type ErrKind int

const (
	ErrInvalidParam ErrKind = iota
	ErrClockRegression
	ErrNotFound
	ErrHoldExpired
	ErrInvalidState
	ErrInsufficientInventory
)

func (k ErrKind) String() string {
	switch k {
	case ErrInvalidParam:
		return "invalid parameter"
	case ErrClockRegression:
		return "clock regression"
	case ErrNotFound:
		return "not found"
	case ErrHoldExpired:
		return "hold expired"
	case ErrInvalidState:
		return "invalid state"
	case ErrInsufficientInventory:
		return "insufficient inventory"
	}
	return "unknown"
}

// Error 描述一次被拒绝的操作。被拒绝的操作不改变任何占用、
// 授权量、时钟或条目状态。
type Error struct {
	Kind    ErrKind
	Segment string     // 库存不足 / 航段不存在时：行程顺序中第一个不满足的航段
	State   EntryState // 状态不符时：条目当前状态（已取消 / 已出票，可区分）
	Detail  string
}

func (e *Error) Error() string {
	switch e.Kind {
	case ErrInsufficientInventory:
		return fmt.Sprintf("insufficient inventory: first failing segment %q", e.Segment)
	case ErrInvalidState:
		return fmt.Sprintf("invalid state: entry is %s (%s)", e.State, e.Detail)
	}
	return e.Kind.String() + ": " + e.Detail
}

// KindOf 提取错误的类别，供调用方按拒绝次序分支处理。
func KindOf(err error) (ErrKind, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind, true
	}
	return 0, false
}

func invalidParamf(format string, args ...any) *Error {
	return &Error{Kind: ErrInvalidParam, Detail: fmt.Sprintf(format, args...)}
}
