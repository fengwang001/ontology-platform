package microgrid

import "fmt"

// Action 表示一个时隙的充放电动作。
type Action int

const (
	ActionIdle Action = iota
	ActionCharge
	ActionDischarge
)

func (a Action) valid() bool {
	return a == ActionIdle || a == ActionCharge || a == ActionDischarge
}

func (a Action) String() string {
	switch a {
	case ActionCharge:
		return "charge"
	case ActionDischarge:
		return "discharge"
	default:
		return "idle"
	}
}

// Mode 表示运行模式：并网或孤岛。
type Mode int

const (
	ModeGrid Mode = iota
	ModeIsland
)

func (m Mode) String() string {
	if m == ModeIsland {
		return "island"
	}
	return "grid"
}

// PlanAction 是单个时隙的计划动作，Amount 为整数电量单位。
type PlanAction struct {
	Action Action
	Amount int
}

// Config 是储能单元与调度规则的参数集合，所有电量为整数单位。
type Config struct {
	Capacity             int // 额定容量
	MinSoC               int // 荷电下限
	MaxSoC               int // 荷电上限
	MaxChargePerSlot     int // 单时隙最大充电电量
	MaxDischargePerSlot  int // 单时隙最大放电电量
	ChargeLossPermille   int // 充电入库折损比例（千分比，0..1000）
	MaintenanceThreshold int // 累计放电吞吐维护阈值（取等即达到）
	ReserveHorizon       int // 备用要求覆盖的后续连续时隙数
	DeviationTolerance   int // 执行偏差容忍量（超过才触发重推演）
	InitialSoC           int // 初始荷电
}

func (cfg Config) validate() error {
	bad := func(msg string) error {
		return &RejectError{Kind: ErrInvalidParam, Slot: -1, Msg: msg}
	}
	switch {
	case cfg.Capacity <= 0:
		return bad("额定容量必须为正")
	case cfg.MinSoC < 0 || cfg.MinSoC > cfg.MaxSoC:
		return bad("荷电上下限非法")
	case cfg.MaxSoC > cfg.Capacity:
		return bad("荷电上限超过额定容量")
	case cfg.MaxChargePerSlot < 0 || cfg.MaxDischargePerSlot < 0:
		return bad("单时隙充放电量上限非法")
	case cfg.ChargeLossPermille < 0 || cfg.ChargeLossPermille > 1000:
		return bad("折损比例越界")
	case cfg.MaintenanceThreshold <= 0:
		return bad("维护阈值必须为正")
	case cfg.ReserveHorizon < 0:
		return bad("备用时隙数非法")
	case cfg.DeviationTolerance < 0:
		return bad("偏差容忍量非法")
	case cfg.InitialSoC < cfg.MinSoC || cfg.InitialSoC > cfg.MaxSoC:
		return bad("初始荷电越界")
	}
	return nil
}

// ErrKind 是可区分的错误类别，声明顺序即固定拒绝次序。
type ErrKind int

const (
	ErrInvalidParam     ErrKind = iota // 参数非法
	ErrSlotMismatch                    // 时隙错误
	ErrMaintenanceLock                 // 维护锁定
	ErrModeNotAllowed                  // 模式不允许
	ErrOutOfBounds                     // 越界
	ErrReserveShortfall                // 备用不足
	ErrForecastMissing                 // 预测缺失
)

func (k ErrKind) String() string {
	switch k {
	case ErrInvalidParam:
		return "参数非法"
	case ErrSlotMismatch:
		return "时隙错误"
	case ErrMaintenanceLock:
		return "维护锁定"
	case ErrModeNotAllowed:
		return "模式不允许"
	case ErrOutOfBounds:
		return "越界"
	case ErrReserveShortfall:
		return "备用不足"
	case ErrForecastMissing:
		return "预测缺失"
	}
	return "未知错误"
}

// RejectError 描述一次被拒绝的操作；Slot 为首个不满足的时隙，非时隙类错误为 -1。
type RejectError struct {
	Kind ErrKind
	Slot int
	Msg  string
}

func (e *RejectError) Error() string {
	return fmt.Sprintf("时隙 %d: %s: %s", e.Slot, e.Kind, e.Msg)
}
