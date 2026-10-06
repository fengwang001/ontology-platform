// Package prepaid 实现预付费电表账户与远程停送电控制器。
//
// 账户余额随电表读数按当时电价扣减，支持一次性低额预警、
// 友好时段保护的推迟停电、每结算周期一次的应急额度、
// 按固定顺序分配的充值以及带确认时限的复电流程。
// 所有金额均为整数，时间用 int64 秒表示的逻辑时钟。
package prepaid

import "fmt"

// State 为停送电状态，任一时刻账户至多处于其中一种。
type State int

const (
	StateSupplyOn       State = iota // 送电
	StatePendingCutoff               // 待停电
	StateCutOff                      // 已停电
	StatePendingRestore              // 待复电
)

func (s State) String() string {
	switch s {
	case StateSupplyOn:
		return "送电"
	case StatePendingCutoff:
		return "待停电"
	case StateCutOff:
		return "已停电"
	case StatePendingRestore:
		return "待复电"
	}
	return "未知状态"
}

// ErrCode 为可区分的错误类别；常量声明顺序即固定的拒绝优先级。
type ErrCode int

const (
	ErrInvalidParam    ErrCode = iota + 1 // 参数非法
	ErrClockRegression                    // 时钟回退
	ErrTimeOrder                          // 时序错误
	ErrReadingRollback                    // 读数倒退
	ErrStateNotAllowed                    // 状态不允许
	ErrAlreadyEnabled                     // 本周期已启用
	ErrConditionNotMet                    // 未达启用条件
	ErrConfirmTimeout                     // 确认超时
)

func (c ErrCode) String() string {
	switch c {
	case ErrInvalidParam:
		return "参数非法"
	case ErrClockRegression:
		return "时钟回退"
	case ErrTimeOrder:
		return "时序错误"
	case ErrReadingRollback:
		return "读数倒退"
	case ErrStateNotAllowed:
		return "状态不允许"
	case ErrAlreadyEnabled:
		return "本周期已启用"
	case ErrConditionNotMet:
		return "未达启用条件"
	case ErrConfirmTimeout:
		return "确认超时"
	}
	return "未知错误"
}

// Error 携带错误类别，可用 CodeOf 提取后按类别断言。
type Error struct{ Code ErrCode }

func (e *Error) Error() string { return e.Code.String() }

func newErr(c ErrCode) *Error { return &Error{Code: c} }

// CodeOf 提取错误的类别；err 为 nil 时返回 0。
func CodeOf(err error) ErrCode {
	if err == nil {
		return 0
	}
	if e, ok := err.(*Error); ok {
		return e.Code
	}
	return -1
}

// EventKind 为事件类别。
type EventKind int

const (
	EvCharge            EventKind = iota // 扣费
	EvOffGridUsage                       // 停电期间用电
	EvWarning                            // 预警
	EvCutoffScheduled                    // 进入待停电
	EvCutoffRescheduled                  // 应急启用后重新判定停电时刻
	EvCutoffCancelled                    // 取消待停电
	EvCutoffExecuted                     // 停电执行（转已停电）
	EvEmergencyEnabled                   // 应急额度启用
	EvRecharge                           // 充值（含三段分配）
	EvPendingRestore                     // 进入待复电
	EvRestored                           // 复电确认
	EvRestoreExpired                     // 确认超时回退已停电
	EvRestoreCancelled                   // 待复电期间余额再跌破而取消
	EvPeriodSummary                      // 周期汇总
	EvPriceSet                           // 电价变更
)

func (k EventKind) String() string {
	switch k {
	case EvCharge:
		return "扣费"
	case EvOffGridUsage:
		return "停电期间用电"
	case EvWarning:
		return "预警"
	case EvCutoffScheduled:
		return "待停电"
	case EvCutoffRescheduled:
		return "停电时刻重判"
	case EvCutoffCancelled:
		return "取消待停电"
	case EvCutoffExecuted:
		return "停电执行"
	case EvEmergencyEnabled:
		return "应急启用"
	case EvRecharge:
		return "充值"
	case EvPendingRestore:
		return "待复电"
	case EvRestored:
		return "复电"
	case EvRestoreExpired:
		return "复电确认超时"
	case EvRestoreCancelled:
		return "取消待复电"
	case EvPeriodSummary:
		return "周期汇总"
	case EvPriceSet:
		return "电价变更"
	}
	return "未知事件"
}

// Event 为账户产生的不可变事件。字段全部为可比较类型，
// 相同操作序列重放得到完全相同的事件序列。
type Event struct {
	Kind           EventKind
	Time           int64 // 事件发生时刻
	Amount         int64 // 扣费金额 / 充值金额 / 应急额度
	Energy         int64 // 电量增量
	Balance        int64 // 事件后余额
	Arrears        int64 // 事件后欠费
	ExecTime       int64 // 停电执行时刻 / 复电确认截止时刻
	Period         int64 // 汇总周期序号
	Charges        int64 // 周期内扣费总额
	Warnings       int64 // 周期内预警次数
	CutoffDuration int64 // 周期内停电时长
	RepayEmergency int64 // 充值中偿还应急额度部分
	RepayArrears   int64 // 充值中清偿欠费部分
	ToBalance      int64 // 充值中进入余额部分
	Price          int64 // 电价（千分之一货币单位/单位电量）
}

func (e Event) String() string {
	return fmt.Sprintf("%s@%d{amt=%d dE=%d bal=%d arr=%d exec=%d per=%d chg=%d warn=%d cut=%d rem=%d rar=%d tobal=%d price=%d}",
		e.Kind, e.Time, e.Amount, e.Energy, e.Balance, e.Arrears, e.ExecTime,
		e.Period, e.Charges, e.Warnings, e.CutoffDuration,
		e.RepayEmergency, e.RepayArrears, e.ToBalance, e.Price)
}

// Config 为账户参数。电价以千分之一货币单位/单位电量计价，
// 扣费金额 = 电量增量 * 电价 / 1000（整数除法即向下取整）。
type Config struct {
	InitialPriceMilli  int64   // 初始电价，> 0，自时刻 0 生效
	WarnThreshold      int64   // 预警阈值，> 0
	RestoreThreshold   int64   // 复电阈值，>= 0
	EmergencyAmount    int64   // 应急额度，> 0
	EmergencyThreshold int64   // 应急启用阈值：余额低于它才可启用
	ArrearsRatioNum    int64   // 欠费清偿比例分子，[0, Den]
	ArrearsRatioDen    int64   // 欠费清偿比例分母，> 0
	ConfirmTimeout     int64   // 复电确认时限，>= 0
	PeriodLength       int64   // 结算周期长度（秒），> 0
	FriendlyStartSec   int64   // 每日友好时段起点（日内秒），[0, 86400)
	FriendlyEndSec     int64   // 每日友好时段终点（日内秒），[0, 86400)；等于起点表示不启用
	RestWeekdays       [7]bool // 每周休息日（全天友好）
	EpochWeekday       int64   // 时刻 0 对应星期几，[0, 7)
}

func (c Config) validate() error {
	switch {
	case c.InitialPriceMilli <= 0,
		c.WarnThreshold <= 0,
		c.RestoreThreshold < 0,
		c.EmergencyAmount <= 0,
		c.ArrearsRatioDen <= 0,
		c.ArrearsRatioNum < 0 || c.ArrearsRatioNum > c.ArrearsRatioDen,
		c.ConfirmTimeout < 0,
		c.PeriodLength <= 0,
		c.FriendlyStartSec < 0 || c.FriendlyStartSec >= secondsPerDay,
		c.FriendlyEndSec < 0 || c.FriendlyEndSec >= secondsPerDay,
		c.EpochWeekday < 0 || c.EpochWeekday >= 7:
		return newErr(ErrInvalidParam)
	}
	return nil
}
