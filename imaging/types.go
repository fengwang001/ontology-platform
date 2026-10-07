package imaging

// MaxTime 是时间轴的上界（含），时间以整数分钟计，取值 [0, MaxTime]。
const MaxTime = 10_000_000

// DayMinutes 是一天的分钟数，质控时段按日重复。
const DayMinutes = 1440

// DeviceCategory 区分 CT 与磁共振两类设备。
type DeviceCategory int

const (
	CT DeviceCategory = iota
	MR
)

func (c DeviceCategory) String() string {
	if c == MR {
		return "MR"
	}
	return "CT"
}

// Config 是系统级配置，构造时给定，之后不可变。
type Config struct {
	RenalValidNormal    int // 普通患者肾功能结果有效期（分钟）
	RenalValidHighRisk  int // 高风险患者肾功能结果有效期（分钟）
	RenalLower          int // 肾功能下限：低于此值报肾功能不足
	RenalUpper          int // 肾功能上限：[下限,上限) 须水化，>= 上限无需水化
	HydrationLead       int // 水化须不晚于预约开始前该提前量
	PremedLead          int // 过敏预处理须不晚于预约开始前该提前量
	ObservationBeds     int // 留观位总数
	ObservationDuration int // 增强检查后留观时长（分钟）
}

// BookingState 是预约的生命周期状态。
type BookingState int

const (
	StateAccepted BookingState = iota
	StateCheckedIn
	StateNeedsReschedule
	StateCancelled
)

func (s BookingState) String() string {
	switch s {
	case StateAccepted:
		return "已受理"
	case StateCheckedIn:
		return "已签到"
	case StateNeedsReschedule:
		return "需改期"
	case StateCancelled:
		return "已取消"
	}
	return "未知"
}

// CheckInOutcome 是一次被接受的签到的结果。
type CheckInOutcome struct {
	State BookingState
	// Reason 仅在 State 为 StateNeedsReschedule 时有意义，
	// 取值为 CodeRenalMissingOrExpired / CodeRenalInsufficient /
	// CodeNotHydrated / CodeNotPremedicated 之一。
	Reason Code
	Detail string
}
