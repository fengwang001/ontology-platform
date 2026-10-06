package station

// Priority 为车辆优先级类别。
type Priority int

const (
	PriorityNormal Priority = iota // 普通
	PriorityFast                   // 优先
)

// State 为会话状态。
type State int

const (
	StateCharging State = iota // 充电中
	StateWaiting               // 等待中
	StateFull                  // 已充满
	StateEnded                 // 已结束
)

func (s State) String() string {
	switch s {
	case StateCharging:
		return "charging"
	case StateWaiting:
		return "waiting"
	case StateFull:
		return "full"
	default:
		return "ended"
	}
}

// Port 描述一个充电接口。
type Port struct {
	ID     string
	MaxPwr int // 接口功率上限
}

// Session 是一次插枪到拔枪的充电会话（快照值）。
type Session struct {
	ID        string
	PortID    string
	PlugOrder int64 // 插枪先后序号，小者先插
	Priority  Priority
	State     State
	Energy    int // 已充电量
	Demand    int // 需求电量
	MinPwr    int // 最低可用功率
	CarMax    int // 车辆最大功率
	Cap       int // 自身上限 = min(车辆最大功率, 接口上限)
	Pwr       int // 当前分配功率（快照）
}

// PlugParams 为插枪参数。
type PlugParams struct {
	SessionID string // 可为空，空则自动生成
	PortID    string
	Demand    int
	CarMax    int
	MinPwr    int
	Priority  Priority
}

// PortView 是快照中的接口视图。
type PortView struct {
	ID        string
	MaxPwr    int
	SessionID string // 空表示空闲
}

// Snapshot 为某时刻充电站的完整可观测状态。
type Snapshot struct {
	Now      int
	Cap      int
	Ports    []PortView
	Sessions []Session
}
