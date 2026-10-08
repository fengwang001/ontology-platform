// Package gateway 实现车联网远程控车指令网关。
//
// 网关受理云端下发的车辆控制指令，依据车辆最新上报状态判定前置条件、
// 互斥关系与唤醒配额，管理指令的下发、回执、过期与幂等。
//
// 时间模型：所有时刻为非负整数秒的逻辑时钟。网关接收到的所有带时刻的
// 操作，其时刻必须不小于上一个被接受操作的时刻（允许相等），否则按
// 时刻回退拒绝。
package gateway

import "fmt"

// Gear 档位。
type Gear int

const (
	GearPark    Gear = iota // 驻车
	GearReverse             // 倒挡
	GearNeutral             // 空挡
	GearDrive               // 前进挡
)

func (g Gear) String() string {
	switch g {
	case GearPark:
		return "park"
	case GearReverse:
		return "reverse"
	case GearNeutral:
		return "neutral"
	case GearDrive:
		return "drive"
	}
	return fmt.Sprintf("gear(%d)", int(g))
}

// PowerState 电源状态。
type PowerState int

const (
	PowerSleep   PowerState = iota // 休眠
	PowerAwake                     // 唤醒
	PowerDriving                   // 行驶
)

func (p PowerState) String() string {
	switch p {
	case PowerSleep:
		return "sleep"
	case PowerAwake:
		return "awake"
	case PowerDriving:
		return "driving"
	}
	return fmt.Sprintf("power(%d)", int(p))
}

// DoorLock 车门锁状态。
type DoorLock int

const (
	LockAllLocked    DoorLock = iota // 全锁
	LockNotAllLocked                 // 非全锁
)

func (l DoorLock) String() string {
	switch l {
	case LockAllLocked:
		return "all_locked"
	case LockNotAllLocked:
		return "not_all_locked"
	}
	return fmt.Sprintf("lock(%d)", int(l))
}

// CmdType 指令类型。
type CmdType int

const (
	CmdUnlock      CmdType = iota // 解锁
	CmdLock                       // 上锁
	CmdACOn                       // 开空调
	CmdACOff                      // 关空调
	CmdFindCar                    // 寻车
	CmdOpenTrunk                  // 开后备箱
	CmdRemoteStart                // 远程启动
)

func (c CmdType) String() string {
	switch c {
	case CmdUnlock:
		return "unlock"
	case CmdLock:
		return "lock"
	case CmdACOn:
		return "ac_on"
	case CmdACOff:
		return "ac_off"
	case CmdFindCar:
		return "find_car"
	case CmdOpenTrunk:
		return "open_trunk"
	case CmdRemoteStart:
		return "remote_start"
	}
	return fmt.Sprintf("cmd(%d)", int(c))
}

// CmdTypeCount 指令类型数量，用于按类型计数。
const CmdTypeCount = 7

// CmdStatus 指令生命周期状态。
type CmdStatus int

const (
	StatusAccepted      CmdStatus = iota // 已受理（等待唤醒完成后下发）
	StatusDispatched                     // 已下发（等待回执）
	StatusSucceeded                      // 回执成功（终态）
	StatusAckFailed                      // 回执失败（终态）
	StatusExpired                        // 过期（终态）
	StatusPrecondFailed                  // 下发前前置条件失效（终态）
	StatusWakeupFailed                   // 唤醒失败（终态）
)

func (s CmdStatus) String() string {
	switch s {
	case StatusAccepted:
		return "accepted"
	case StatusDispatched:
		return "dispatched"
	case StatusSucceeded:
		return "succeeded"
	case StatusAckFailed:
		return "ack_failed"
	case StatusExpired:
		return "expired"
	case StatusPrecondFailed:
		return "precond_failed"
	case StatusWakeupFailed:
		return "wakeup_failed"
	}
	return fmt.Sprintf("status(%d)", int(s))
}

// Terminal 报告状态是否为终态。
func (s CmdStatus) Terminal() bool {
	return s >= StatusSucceeded
}

// RejectReason 提交被拒绝的原因，按判定优先级排列。
type RejectReason int

const (
	RejectInvalidParam        RejectReason = iota // 参数非法
	RejectTimeRegression                          // 时刻回退
	RejectUnknownVehicle                          // 车辆未知
	RejectIdempotencyConflict                     // 幂等冲突
	RejectMutexConflict                           // 互斥冲突
	RejectPrecondition                            // 前置不满足
	RejectWakeupQuota                             // 唤醒配额耗尽
)

func (r RejectReason) String() string {
	switch r {
	case RejectInvalidParam:
		return "invalid_param"
	case RejectTimeRegression:
		return "time_regression"
	case RejectUnknownVehicle:
		return "unknown_vehicle"
	case RejectIdempotencyConflict:
		return "idempotency_conflict"
	case RejectMutexConflict:
		return "mutex_conflict"
	case RejectPrecondition:
		return "precondition"
	case RejectWakeupQuota:
		return "wakeup_quota"
	}
	return fmt.Sprintf("reason(%d)", int(r))
}

// RejectError 表示一次被拒绝的操作。被拒绝的操作不改变任何状态与
// 时钟，不消耗唤醒配额，也不占用幂等键。
type RejectError struct {
	Reason RejectReason
	Detail string
}

func (e *RejectError) Error() string {
	if e.Detail == "" {
		return "rejected: " + e.Reason.String()
	}
	return "rejected: " + e.Reason.String() + ": " + e.Detail
}

func rejectf(reason RejectReason, format string, args ...any) *RejectError {
	return &RejectError{Reason: reason, Detail: fmt.Sprintf(format, args...)}
}

// StateReport 车端状态上报。
type StateReport struct {
	Seq        int64      // 车端序号，单调递增
	Time       int64      // 上报时刻（秒）
	Gear       Gear       // 档位
	SpeedKmh   int        // 车速 km/h，非负
	Power      PowerState // 电源状态
	Lock       DoorLock   // 车门锁状态
	BatteryPct int        // 电量百分比 0..100
}

// SubmitRequest 指令提交请求。
type SubmitRequest struct {
	VehicleID   string  // 目标车辆
	Submitter   string  // 提交者（幂等键组成部分）
	RequestID   string  // 请求编号（幂等键组成部分）
	Type        CmdType // 指令类型
	Time        int64   // 提交时刻（秒）
	ValiditySec int64   // 有效期（秒），自受理时刻起算，必须为正
}

// SubmitResult 提交受理结果。
type SubmitResult struct {
	CommandID string    // 指令 ID
	Status    CmdStatus // 受理后的即时状态（Accepted 或 Dispatched）
	Duplicate bool      // 是否为幂等重放（返回原指令结果）
}

// ReportOutcome 状态上报的处理结果。
type ReportOutcome int

const (
	ReportAccepted ReportOutcome = iota // 已接受，状态更新
	ReportDropped                       // 序号不大于已接受最大序号，丢弃
)

func (o ReportOutcome) String() string {
	if o == ReportAccepted {
		return "accepted"
	}
	return "dropped"
}

// AckResult 回执处理结果。
type AckResult struct {
	Late bool // 是否为迟到回执（指令已终结，仅计数）
}

// CommandView 指令的可查询快照。
type CommandView struct {
	ID           string
	VehicleID    string
	Submitter    string
	RequestID    string
	Type         CmdType
	AcceptTime   int64
	ValiditySec  int64
	Status       CmdStatus
	LateAcks     int   // 迟到回执次数
	DispatchTime int64 // 下发时刻，未下发为 -1
	FinishTime   int64 // 终结时刻，未终结为 -1
}

// VehicleSnapshot 车辆诊断快照，用于测试与运维观测。
type VehicleSnapshot struct {
	VehicleID     string
	Registered    bool
	HasReport     bool
	LastReport    StateReport
	InFlight      int             // 在途指令总数
	InFlightByTyp map[CmdType]int // 按类型统计的在途数
	CommandTotal  int             // 历史指令总数（含已终结）
	WakeupActive  bool            // 是否有唤醒进行中
	WakeDay       int64           // 最近一次唤醒所属自然日
	WakeUsedToday int             // 当日已消耗唤醒配额
}

// Config 网关配置。
type Config struct {
	MinBatteryPct           int   // 电量下限（开空调/远程启动）
	StalenessThresholdSec   int64 // 状态陈旧阈值（秒），超过视为未知，恰等于不算陈旧
	WakeupTimeoutSec        int64 // 唤醒超时（秒），超过仍未收到上线上报则唤醒失败
	WakeupQuotaPerDay       int   // 每车每自然日唤醒次数上限
	DayOffsetSec            int64 // 自然日界秒级时区偏移
	IdempotencyRetentionSec int64 // 幂等记录保留时长（秒），超过后同键视为新请求
}

// DefaultConfig 返回一组合理的默认配置。
func DefaultConfig() Config {
	return Config{
		MinBatteryPct:           20,
		StalenessThresholdSec:   300,
		WakeupTimeoutSec:        60,
		WakeupQuotaPerDay:       10,
		DayOffsetSec:            8 * 3600, // 默认东八区
		IdempotencyRetentionSec: 24 * 3600,
	}
}

func (c Config) validate() error {
	if c.MinBatteryPct < 0 || c.MinBatteryPct > 100 {
		return fmt.Errorf("MinBatteryPct 须在 [0,100] 内: %d", c.MinBatteryPct)
	}
	if c.StalenessThresholdSec < 0 {
		return fmt.Errorf("StalenessThresholdSec 须非负: %d", c.StalenessThresholdSec)
	}
	if c.WakeupTimeoutSec < 0 {
		return fmt.Errorf("WakeupTimeoutSec 须非负: %d", c.WakeupTimeoutSec)
	}
	if c.WakeupQuotaPerDay <= 0 {
		return fmt.Errorf("WakeupQuotaPerDay 须为正: %d", c.WakeupQuotaPerDay)
	}
	if c.IdempotencyRetentionSec < 0 {
		return fmt.Errorf("IdempotencyRetentionSec 须非负: %d", c.IdempotencyRetentionSec)
	}
	return nil
}

// dayOf 计算时刻所属自然日序号（按配置的秒级时区偏移）。
func (c Config) dayOf(t int64) int64 {
	v := t + c.DayOffsetSec
	// 向下取整除法，兼容偏移为负的情形。
	if v >= 0 {
		return v / 86400
	}
	return -((-v + 86399) / 86400)
}
