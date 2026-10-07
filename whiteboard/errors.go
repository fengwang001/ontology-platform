// Package whiteboard 实现多人实时协作白板的元素叠放与编辑锁服务。
//
// 包内模块划分：
//   - errors.go  错误分类与拒绝优先级
//   - treap.go   隐式笛卡尔树（order-statistics tree），支撑 O(log n) 的全序维护
//   - order.go   元素全序（底到顶）的维护：段移动、名次、区间查询
//   - locks.go   软锁表：O(1) 的加锁/解锁/查询
//   - board.go   Board 服务：全部公开操作、校验顺序、修订号与时钟
package whiteboard

import "fmt"

// Kind 表示操作被拒绝的类别。
//
// 常量声明顺序即拒绝优先级（数值越小优先级越高）：
// 参数非法 > 时钟回退 > 目标或参照不存在 > 目标与参照不合法 > 被他人锁定 > 版本冲突。
// 一次操作同时违反多类约束时，只报告优先级最高（数值最小）的第一个。
type Kind int

const (
	// ErrInvalidParam 参数非法：空 user/id、now 越界、ttl 越界、成员数越界、
	// 标识重复、成员表含重复元素等。
	ErrInvalidParam Kind = iota
	// ErrClock 时钟回退：now 小于上一次被接受操作的 now。
	ErrClock
	// ErrNotFound 目标或参照不存在。
	ErrNotFound
	// ErrInvalidTarget 目标与参照不合法：参照在移动集合内、直接移动组合成员、
	// 组合成员重叠、目标类型错误（如对元素 Ungroup）等。
	ErrInvalidTarget
	// ErrLocked 被他人持有未到期锁。Error.Holder 与 Error.Remaining 有效。
	ErrLocked
	// ErrConflict 乐观版本冲突：移动集合中某元素的最近影响修订号大于 baseRev。
	ErrConflict
)

// Error 是被拒绝操作返回的错误类型。
type Error struct {
	Kind Kind
	Msg  string
	// Holder 与 Remaining 仅在 Kind == ErrLocked 时有意义：
	// Holder 为锁持有者，Remaining 为距到期的剩余秒数（> 0）。
	Holder    string
	Remaining int64
}

func (e *Error) Error() string {
	if e.Kind == ErrLocked {
		return fmt.Sprintf("%s (held by %q, %ds remaining)", e.Msg, e.Holder, e.Remaining)
	}
	return e.Msg
}

func newErr(kind Kind, format string, args ...any) *Error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

func lockedErr(holder string, remaining int64, format string, args ...any) *Error {
	return &Error{
		Kind:      ErrLocked,
		Msg:       fmt.Sprintf(format, args...),
		Holder:    holder,
		Remaining: remaining,
	}
}
