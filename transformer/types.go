// Package transformer 实现变压器容量预约系统：变压器容量按生效时刻可变，
// 馈线上限固定，预约先占位、限时确认，两级（馈线/变压器）逐时刻校验。
package transformer

import "fmt"

// Status 预约状态机。
type Status int

const (
	StatusHolding   Status = iota // 占位中
	StatusConfirmed               // 已确认
	StatusExpired                 // 已失效（占位到期未确认）
	StatusCancelled               // 已取消（容量下调被挤占）
	StatusCompleted               // 已完成（区间终点已过）
	StatusReleased                // 已释放（主动释放）
)

func (s Status) String() string {
	switch s {
	case StatusHolding:
		return "占位中"
	case StatusConfirmed:
		return "已确认"
	case StatusExpired:
		return "已失效"
	case StatusCancelled:
		return "已取消"
	case StatusCompleted:
		return "已完成"
	case StatusReleased:
		return "已释放"
	}
	return "未知"
}

// Level 容量不足的层级。
type Level int

const (
	LevelNone Level = iota
	LevelFeeder
	LevelTransformer
)

func (l Level) String() string {
	switch l {
	case LevelFeeder:
		return "馈线"
	case LevelTransformer:
		return "变压器"
	}
	return "无"
}

// ErrCategory 错误类别，拒绝次序即声明次序（靠前者优先）。
type ErrCategory int

const (
	ErrInvalidParam ErrCategory = iota // 参数非法
	ErrClockRewind                     // 时钟回退
	ErrNotFound                        // 预约不存在
	ErrInvalidState                    // 状态不允许
	ErrHoldExpired                     // 占位已到期
	ErrCancelled                       // 已取消
	ErrCapacity                        // 容量不足
)

func (c ErrCategory) String() string {
	switch c {
	case ErrInvalidParam:
		return "参数非法"
	case ErrClockRewind:
		return "时钟回退"
	case ErrNotFound:
		return "预约不存在"
	case ErrInvalidState:
		return "状态不允许"
	case ErrHoldExpired:
		return "占位已到期"
	case ErrCancelled:
		return "已取消"
	case ErrCapacity:
		return "容量不足"
	}
	return "未知错误"
}

// Error 为携带判定依据的结构化错误。Level 与 Time 仅对 ErrCapacity 有意义。
type Error struct {
	Category ErrCategory
	Level    Level
	Time     int
	Detail   string
}

func (e *Error) Error() string {
	if e.Category == ErrCapacity {
		return fmt.Sprintf("%s：首个不满足时刻 %d，层级 %s（%s）", e.Category, e.Time, e.Level, e.Detail)
	}
	return fmt.Sprintf("%s：%s", e.Category, e.Detail)
}

// Reservation 预约记录。区间为左闭右开 [Start, End)。
type Reservation struct {
	ID            int
	Feeder        string
	Start         int
	End           int
	Power         int
	Status        Status
	CreatedAt     int
	HoldExpiresAt int // 占位到期时刻，等于该时刻即视为已到期
}

// Config 系统配置。
type Config struct {
	HoldDuration int // 占位时长
}
