package retention

import (
	"fmt"
	"sync"
	"time"
)

// State 表示对象的逻辑删除状态。
type State int

const (
	StateAlive    State = iota // 存活
	StateGrace                 // 待撤销宽限
	StateFrozen                // 保留期冻结
	StateArchived              // 已归档
)

func (st State) String() string {
	switch st {
	case StateAlive:
		return "ALIVE"
	case StateGrace:
		return "GRACE"
	case StateFrozen:
		return "FROZEN"
	case StateArchived:
		return "ARCHIVED"
	default:
		return "UNKNOWN"
	}
}

// Role 表示查询身份。
type Role int

const (
	RoleUser  Role = iota // 一般使用者
	RoleAdmin             // 数据管理员
)

func (r Role) String() string {
	switch r {
	case RoleUser:
		return "USER"
	case RoleAdmin:
		return "ADMIN"
	default:
		return "UNKNOWN"
	}
}

// ErrorCode 为四类互斥错误之一，判定次序固定为
// ErrNotFound -> ErrIllegalTransition -> ErrInvalidParameter -> ErrFrozenNotExpired。
type ErrorCode int

const (
	ErrNotFound ErrorCode = iota + 1
	ErrIllegalTransition
	ErrInvalidParameter
	ErrFrozenNotExpired
)

func (c ErrorCode) String() string {
	switch c {
	case ErrNotFound:
		return "OBJECT_NOT_FOUND"
	case ErrIllegalTransition:
		return "ILLEGAL_TRANSITION"
	case ErrInvalidParameter:
		return "INVALID_PARAMETER"
	case ErrFrozenNotExpired:
		return "FROZEN_NOT_EXPIRED"
	default:
		return "OK"
	}
}

// ServiceError 携带四类互斥错误码。
type ServiceError struct {
	code ErrorCode
	msg  string
}

func (e *ServiceError) Error() string { return e.code.String() + ": " + e.msg }

// Code 提取错误码；err 为 nil 时返回 0。
func Code(err error) ErrorCode {
	if err == nil {
		return 0
	}
	if se, ok := err.(*ServiceError); ok {
		return se.code
	}
	return 0
}

func errf(code ErrorCode, format string, args ...any) error {
	return &ServiceError{code: code, msg: fmt.Sprintf(format, args...)}
}

// Visibility 是一次可见性判定的结果。
//
// Exists 表示对象在服务中是否存在（存在性对两种身份一致）。
// Visible 表示该身份是否“看得到”该对象（含业务属性）。
// 管理员在冻结状态下 Visible 为 true 但 Attributes 为 nil：
// 只能看到状态本身与冻结截止时刻，看不到业务属性取值。
type Visibility struct {
	Visible        bool
	Exists         bool
	State          State
	GraceDeadline  int64
	FreezeDeadline int64
	Attributes     map[string]string
}

// Clock 提供当前时刻，时刻为不透明的单调整数（如 Unix 毫秒）。
type Clock interface{ Now() int64 }

// LogicalClock 是可显式推进的单调逻辑时钟，供测试复现临界时刻。
// 被拒绝的转换绝不推进时钟；只有 Advance 能改变当前时刻。
type LogicalClock struct {
	mu  sync.Mutex
	now int64
}

func NewLogicalClock(start int64) *LogicalClock { return &LogicalClock{now: start} }

func (c *LogicalClock) Now() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance 把时钟推进到 t；时钟单调，t 早于当前时刻时忽略，返回当前时刻。
func (c *LogicalClock) Advance(t int64) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t > c.now {
		c.now = t
	}
	return c.now
}

type systemClock struct{}

func (systemClock) Now() int64 { return time.Now().UnixMilli() }
