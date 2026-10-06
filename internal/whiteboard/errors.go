// Package whiteboard 实现多人协作白板的元素叠放次序、组合整体移动、
// 软锁（TTL）与乐观版本冲突控制。
package whiteboard

import "fmt"

// RejectKind 标识拒绝操作的原因类别。类别之间具有固定的先后次序，
// 一次操作只报告最先命中的一个类别。
type RejectKind int

const (
	KindInvalidArgument RejectKind = iota + 1 // 参数非法
	KindClockBackward                         // 时钟回退
	KindNotFound                              // 目标或参照不存在
	KindIllegal                               // 目标与参照不合法
	KindLocked                                // 被他人持有未到期锁
	KindConflict                              // 乐观版本冲突
)

func (k RejectKind) String() string {
	switch k {
	case KindInvalidArgument:
		return "invalid-argument"
	case KindClockBackward:
		return "clock-backward"
	case KindNotFound:
		return "not-found"
	case KindIllegal:
		return "illegal-operation"
	case KindLocked:
		return "locked"
	case KindConflict:
		return "version-conflict"
	default:
		return fmt.Sprintf("reject-%d", int(k))
	}
}

// LockError 表示“被他人锁定”拒绝，可区分持有者与剩余秒数。
// 剩余秒数按过期时刻减去当前 now 计算，至少为 1。
type LockError struct {
	Holder   string
	ExpireAt int64
	Remain   int64
	TargetID string
}

func (e *LockError) Error() string {
	return fmt.Sprintf("locked: id=%q holder=%q remain=%ds", e.TargetID, e.Holder, e.Remain)
}

// RejectError 是除被他人锁定之外的所有拒绝原因。
type RejectError struct {
	Kind RejectKind
	Msg  string
}

func (e *RejectError) Error() string {
	return e.Kind.String() + ": " + e.Msg
}

func reject(kind RejectKind, format string, args ...any) error {
	return &RejectError{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

// Side 表示 Reorder 时相对锚点的落点。
type Side int

const (
	Below Side = -1 // 紧贴锚点下方
	Above Side = 1  // 紧贴锚点上方
)

// LockState 是锁的快照视图（仅包含判定时刻未到期的锁）。
type LockState struct {
	TargetID string
	Holder   string
	ExpireAt int64
}
