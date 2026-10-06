// Package meter 实现预付费电表账户与远程停送电控制：
// 区间计费、一次性滞回预警、友好时段推迟停电、每周期一次应急额度、
// 三段式充值分配、滞回复电与用户确认、周期汇总与跨周期停电时长切分。
//
// 所有公开方法可并发调用（内部互斥），结果等价于某个串行执行顺序。
// 错误以哨兵值返回，拒绝次序固定：
// 参数非法 > 时钟回退 > 时序错误 > 读数倒退 > 状态不允许 >
// 本周期已启用 > 未达启用条件 > 确认超时。
// 任何被拒绝的操作不改变余额、欠费、状态、事件与时钟。
package meter

import "sync"

import "errors"

var (
	ErrInvalidParam   = errors.New("参数非法")
	ErrClockBack      = errors.New("时钟回退")
	ErrOrder          = errors.New("时序错误")
	ErrReadingBack    = errors.New("读数倒退")
	ErrStateForbidden = errors.New("状态不允许")
	ErrAlreadyUsed    = errors.New("本周期已启用")
	ErrNotEligible    = errors.New("未达启用条件")
	ErrConfirmTimeout = errors.New("确认超时")
)

type Money = int64
type Tick = int64

type Status int

const (
	Powered Status = iota
	PendingCut
	Cut
	PendingRestore
)

func (s Status) String() string {
	switch s {
	case Powered:
		return "powered"
	case PendingCut:
		return "pending_cut"
	case Cut:
		return "cut"
	case PendingRestore:
		return "pending_restore"
	}
	return "unknown"
}

type Reading struct {
	At         Tick
	Cumulative int64
}

type Ratio struct {
	N int64
	D int64
}

type EventKind int

const (
	EvCreated EventKind = iota
	EvDeduct
	EvDuringCutUsage
	EvWarn
	EvPendingCut
	EvCut
	EvCutCanceled
	EvEmergencyGranted
	EvRecharge
	EvPendingRestore
	EvRestoreCanceled
	EvRestoreTimeout
	EvRestored
	EvCycleSummary
)

type Event struct {
	Kind EventKind
	At   Tick

	CumulativeBefore int64
	Energy           int64
	Price            int64
	Amount           int64
	Balance          int64
	Debt             int64

	ExecuteAt Tick
	Deadline  Tick

	Granted       int64
	Recharge      int64
	EmergencyPaid int64
	DebtPaid      int64
	ToBalance     int64

	Cycle       int64
	CycleStart  Tick
	CycleEnd    Tick
	Deducted    int64
	Warns       int
	CutDuration int64
}

type Snapshot struct {
	Now                       Tick
	Status                    Status
	Balance                   Money
	Debt                      Money
	EmergencyUsed             Money
	CutExecuteAt              Tick
	RestoreDeadline           Tick
	Warned                    bool
	EmergencyEnabledThisCycle bool
	CycleIndex                int64

	TotalRecharge         Money
	TotalEmergencyGranted Money
	TotalDeducted         Money
	TotalEmergencyRepaid  Money

	CycleDeducted    Money
	CycleWarns       int
	CycleCutDuration int64

	Events []Event
}

type Controller struct {
	mu  sync.Mutex
	cfg Config

	now    Tick
	status Status

	balance       Money
	debt          Money
	emergencyUsed Money

	cutExecuteAt    Tick
	restoreDeadline Tick

	lastReading *Reading
	warned      bool

	cycleIndex         int64
	emergencyUsedCycle bool

	cycleDeducted    Money
	cycleWarns       int
	cycleCutDuration int64
	cutAccumStart    Tick
	offerDeadline    Tick

	totalRecharge         Money
	totalEmergencyGranted Money
	totalDeducted         Money
	totalEmergencyRepaid  Money

	events []Event
}
