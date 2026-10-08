// Package eventloop 实现一个浏览器事件循环的任务调度内核。
//
// 内核由五部分协作构成：任务源队列、微任务检查点、渲染机会、
// 空闲期与定时器。时间只由注入时钟驱动（AdvanceTo），在注入时钟下
// 任意任务、微任务、动画帧回调、空闲回调与定时器的执行次序
// 唯一确定，并输出带类型、句柄与时刻的执行轨迹。
package eventloop

import "fmt"

// Source 是任务源。声明次序即跨源并列时的固定打破次序。
type Source int

const (
	SourceUserInteraction Source = iota // 用户交互
	SourceTimer                         // 定时器
	SourceNetwork                       // 网络
	SourceMessage                       // 消息
	SourceInternal                      // 内部
)

const numSources = 5

// NoSource 用于非任务类轨迹条目的 Source 占位。
const NoSource Source = -1

func (s Source) valid() bool { return s >= 0 && s < numSources }

func (s Source) String() string {
	switch s {
	case SourceUserInteraction:
		return "user-interaction"
	case SourceTimer:
		return "timer"
	case SourceNetwork:
		return "network"
	case SourceMessage:
		return "message"
	case SourceInternal:
		return "internal"
	}
	return fmt.Sprintf("source(%d)", int(s))
}

// ExecType 是执行轨迹条目的类型。
type ExecType int

const (
	ExecTask ExecType = iota
	ExecMicrotask
	ExecAnimationFrame
	ExecIdle
)

func (t ExecType) String() string {
	switch t {
	case ExecTask:
		return "task"
	case ExecMicrotask:
		return "microtask"
	case ExecAnimationFrame:
		return "animation-frame"
	case ExecIdle:
		return "idle"
	}
	return fmt.Sprintf("exec(%d)", int(t))
}

// Handle 是每次注册返回的唯一句柄，用于取消与轨迹追踪。
type Handle uint64

// TraceEntry 记录一次回调执行。Reason 给出本次选择的判定依据。
type TraceEntry struct {
	Type   ExecType
	Source Source // 仅 Type == ExecTask 时有效，否则为 NoSource
	Handle Handle
	Time   int64
	Reason string
}

func (e TraceEntry) String() string {
	if e.Type == ExecTask {
		return fmt.Sprintf("%s/%s h=%d t=%d (%s)", e.Type, e.Source, e.Handle, e.Time, e.Reason)
	}
	return fmt.Sprintf("%s h=%d t=%d (%s)", e.Type, e.Handle, e.Time, e.Reason)
}

// ErrorReport 记录回调抛出的异常，报告次序等于抛出次序。
type ErrorReport struct {
	Time   int64
	Handle Handle
	Value  any
}

// IdleDeadline 传给空闲回调：剩余时间与截止时刻（下一帧边界）。
type IdleDeadline struct {
	Remaining  int64
	Deadline   int64
	DidTimeout bool
}

// NoTimeout 表示空闲回调不设置超时。
const NoTimeout int64 = int64(^uint64(0) >> 1)

// Config 是内核的可配置参数。
type Config struct {
	StarvationLimit       int   // 队首任务允许被跳过的最大轮次数
	FrameInterval         int64 // 帧间隔，必须为正
	MinTimerDelay         int64 // 定时器最小延迟
	TimerNestingThreshold int   // 嵌套深度阈值，超过才钳制（等于不钳制）
	TimerClampDelay       int64 // 超阈值定时器的钳制延迟
}

// DefaultConfig 返回一组常用默认参数。
func DefaultConfig() Config {
	return Config{
		StarvationLimit:       4,
		FrameInterval:         16,
		MinTimerDelay:         1,
		TimerNestingThreshold: 5,
		TimerClampDelay:       4,
	}
}

// Category 是可区分的错误类别。
type Category int

const (
	ErrInvalidArg      Category = iota // 参数非法
	ErrClockRollback                   // 时钟回退
	ErrHandleNotFound                  // 句柄不存在
	ErrDuplicateCancel                 // 重复取消
)

func (c Category) String() string {
	switch c {
	case ErrInvalidArg:
		return "invalid-argument"
	case ErrClockRollback:
		return "clock-rollback"
	case ErrHandleNotFound:
		return "handle-not-found"
	case ErrDuplicateCancel:
		return "duplicate-cancel"
	}
	return fmt.Sprintf("category(%d)", int(c))
}

// Error 是内核返回的错误，携带可区分的类别。
// 校验次序固定为：参数非法 → 时钟回退 → 句柄不存在 → 重复取消。
type Error struct {
	Category Category
	Message  string
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Category, e.Message) }
