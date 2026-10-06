// Package alarm implements an industrial control-room alarm lifecycle service.
package alarm

// Priority 表示报警优先级。
type Priority int

const (
	PriorityEmergency Priority = 3 // 紧急
	PriorityHigh      Priority = 2 // 高
	PriorityLow       Priority = 1 // 低
)

func (p Priority) String() string {
	switch p {
	case PriorityEmergency:
		return "emergency"
	case PriorityHigh:
		return "high"
	case PriorityLow:
		return "low"
	default:
		return "invalid"
	}
}

// State 是报警点的基本状态。
type State int

const (
	StateNormal        State = 0 // 正常
	StateActiveUnacked State = 1 // 激活未确认
	StateActiveAcked   State = 2 // 激活已确认
	StateReturnUnacked State = 3 // 返回未确认
)

func (s State) String() string {
	switch s {
	case StateNormal:
		return "normal"
	case StateActiveUnacked:
		return "active-unacked"
	case StateActiveAcked:
		return "active-acked"
	case StateReturnUnacked:
		return "return-unacked"
	default:
		return "invalid"
	}
}

// Role 表示操作者角色。工程师拥有操作员的全部权限。
type Role int

const (
	RoleOperator Role = 1 // 操作员
	RoleEngineer Role = 2 // 工程师
)

func (r Role) String() string {
	switch r {
	case RoleOperator:
		return "operator"
	case RoleEngineer:
		return "engineer"
	default:
		return "invalid"
	}
}

// PointConfig 是一个报警点的静态配置。
// SuppressConditions 为抑制条件（工况名）的集合：任一工况为真即处于抑制。
type PointConfig struct {
	ID                 int
	Priority           Priority
	SuppressConditions []string
}

// Config 是系统级配置。
// ChatWindowSec 为震荡滑动窗口长度（秒，左开右闭）；ChatCount 为窗口内进入激活
// 次数达到该值即自动屏蔽；ChatShelveSec 为震荡自动屏蔽时长。
// HighShelveMaxSec / LowShelveMaxSec 分别为高 / 低优先级手动屏蔽时长上限。
type Config struct {
	ChatWindowSec    int
	ChatCount        int
	ChatShelveSec    int
	HighShelveMaxSec int
	LowShelveMaxSec  int
}

// Shelving 描述一个报警点当前的屏蔽信息。
type Shelving struct {
	Active bool
	Manual bool   // true 为手动屏蔽，false 为震荡自动屏蔽
	Until  int    // 解除时刻（屏蔽在该时刻生效；Until 之后仍为屏蔽）
	Reason string // 屏蔽原因；自动屏蔽恒为 ChatReason
}

// ChatReason 是震荡自动屏蔽记录的原因。
const ChatReason = "chattering"

// ActiveAlarm 是活动列表中的一条。
type ActiveAlarm struct {
	ID           int
	Priority     Priority
	State        State
	LastActiveAt int
}

// activePoint 是一个报警点的全部运行时状态（字段均由 service.mu 保护）。
type activePoint struct {
	id       int
	priority Priority
	conds    map[string]struct{}

	state        State
	lastActiveAt int // 最近一次“进入激活”的时刻

	shelved     bool
	manual      bool
	shelveUntil int
	reason      string

	suppressed bool // 当前是否处于条件抑制
	disabled   bool // 是否被停用

	inTree bool // 是否已在活动列表树中

	// 震荡窗口：左开右闭 (now-window, now] 内“进入激活”的时刻，升序。
	chat []int
}
