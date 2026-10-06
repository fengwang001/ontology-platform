package charger

// Priority 为车辆的优先级类别。
type Priority int

const (
	Normal Priority = 0 // 普通
	Prefer Priority = 1 // 优先
)

// State 为会话状态。
type State int

const (
	StateWaiting  State = iota // 等待中
	StateCharging              // 充电中
	StateFull                  // 已充满（仍占用接口）
	StateEnded                 // 已结束（拔枪）
)

func (s State) String() string {
	switch s {
	case StateWaiting:
		return "等待中"
	case StateCharging:
		return "充电中"
	case StateFull:
		return "已充满"
	default:
		return "已结束"
	}
}

// Port 是一个充电接口，带上限功率。
type Port struct {
	ID  string
	Cap int // 接口功率上限（正整数）
}

// Session 是一次插枪到拔枪的会话。
// 字段只读快照语义：Station 内部持有的是同一结构体指针，
// 调用方应通过 Status() 获取快照，不要长期持有。
type Session struct {
	ID       int64
	PortID   string
	PlugAt   int64    // 插枪时刻（秒）
	Need     int      // 需求电量
	MaxP     int      // 车辆最大功率
	MinP     int      // 最低可用功率
	Prio     Priority // 当前优先级（可变更，插枪序不变）
	Charged  int      // 已充电量
	Power    int      // 当前分配功率（充满/等待时为 0）
	State    State
	UnplugAt int64 // 拔枪时刻，未拔枪为 -1
	FullAt   int64 // 充满时刻，未充满为 -1

	selfCap int  // min(车辆最大功率, 接口上限)
	active  bool // 仍在站（未拔枪）
}

// Snapshot 是会话的只读快照。
type Snapshot struct {
	ID       int64
	PortID   string
	PlugAt   int64
	Need     int
	MaxP     int
	MinP     int
	Prio     Priority
	Charged  int
	Power    int
	State    State
	UnplugAt int64
	FullAt   int64
}

func (s *Session) snapshot() Snapshot {
	return Snapshot{
		ID: s.ID, PortID: s.PortID, PlugAt: s.PlugAt, Need: s.Need,
		MaxP: s.MaxP, MinP: s.MinP, Prio: s.Prio, Charged: s.Charged,
		Power: s.Power, State: s.State, UnplugAt: s.UnplugAt, FullAt: s.FullAt,
	}
}

// FillEvent 描述一次推进中发生的一台车充满事件。
type FillEvent struct {
	SessionID int64
	At        int64
}

// Status 是充电站整体快照。
type Status struct {
	Now      int64 // 当前时钟（秒）
	TotalCap int   // 当前总功率上限
	Sessions []Snapshot
}

// Logger 记录每条操作输入、输出与判定依据（用于可复现日志）。
type Logger interface {
	Log(line string)
}
