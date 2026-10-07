// Package fence implements a shared-bike geofence service: nested fence
// attribution (operating / no-parking / reward zones), return & fee rules,
// dispatch task lifecycle and daily reward limits. All operations are
// serialized by a single mutex, so concurrent calls are equivalent to some
// serial order, and replaying the same accepted operation sequence yields
// identical state, fees and task history.
package fence

import "fmt"

// ErrCode identifies the single error class reported by a rejected
// operation. Codes are ordered by reporting priority: when several error
// conditions hold simultaneously, the one with the smallest code is
// returned.
type ErrCode int

const (
	ErrInvalidArgument    ErrCode = iota + 1 // 参数非法
	ErrClockRollback                         // 时钟回退
	ErrFenceNotFound                         // 围栏不存在
	ErrFenceConstraint                       // 围栏约束不合法
	ErrVehicleNotFound                       // 车辆不存在
	ErrVehicleNotRiding                      // 车辆不在骑行中
	ErrNoParkingReturn                       // 禁停区还车
	ErrFenceFull                             // 围栏已满
	ErrTaskNotFound                          // 任务不存在
	ErrTaskAlreadyClaimed                    // 任务已被认领
	ErrTaskNotInProgress                     // 任务不在处理中
	ErrInvalidDropPoint                      // 落点不合法
)

var errCodeNames = map[ErrCode]string{
	ErrInvalidArgument:    "参数非法",
	ErrClockRollback:      "时钟回退",
	ErrFenceNotFound:      "围栏不存在",
	ErrFenceConstraint:    "围栏约束不合法",
	ErrVehicleNotFound:    "车辆不存在",
	ErrVehicleNotRiding:   "车辆不在骑行中",
	ErrNoParkingReturn:    "禁停区还车",
	ErrFenceFull:          "围栏已满",
	ErrTaskNotFound:       "任务不存在",
	ErrTaskAlreadyClaimed: "任务已被认领",
	ErrTaskNotInProgress:  "任务不在处理中",
	ErrInvalidDropPoint:   "落点不合法",
}

func (c ErrCode) String() string { return errCodeNames[c] }

// Error is the only error type returned by Service operations.
type Error struct {
	Code ErrCode
	Msg  string
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Msg) }

func codeError(code ErrCode, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// FenceType classifies a fence.
type FenceType int

const (
	Outside   FenceType = iota // not a fence: point attribution for open area
	Operating                  // 运营区
	NoParking                  // 禁停区
	Reward                     // 奖励区
)

func (t FenceType) String() string {
	switch t {
	case Operating:
		return "运营区"
	case NoParking:
		return "禁停区"
	case Reward:
		return "奖励区"
	default:
		return "区外"
	}
}

// Point is an integer coordinate on the plane.
type Point struct {
	X, Y int64
}

// Fence is a simple polygon with a vehicle capacity.
type Fence struct {
	ID       string
	Type     FenceType
	Vertices []Point
	Capacity int

	parentID string // containing operating fence, for inner fences
	minX     int64
	minY     int64
	maxX     int64
	maxY     int64
}

// Config holds the tunable rules of the service.
type Config struct {
	Timezone                string // IANA name used to split natural days
	OutsideFee              int64  // fee charged for an outside return
	RewardAmount            int64  // reward granted for a reward-zone return
	DefaultOperatingFenceID string // planned destination of recall tasks
	EvacuationNum           int64  // trigger ratio numerator (count/capacity >= num/den)
	EvacuationDen           int64  // trigger ratio denominator
	ClaimTimeoutSec         int64  // claim expires when elapsed >= this many seconds
}

// VehicleState is the lifecycle state of a bike.
type VehicleState int

const (
	VehicleIdle      VehicleState = iota + 1 // registered, not riding, not parked
	VehicleRiding                            // unlocked, being ridden
	VehicleParked                            // attributed to a fence
	VehicleInTransit                         // returned outside, awaiting recall
)

func (s VehicleState) String() string {
	switch s {
	case VehicleIdle:
		return "空闲"
	case VehicleRiding:
		return "骑行中"
	case VehicleParked:
		return "已停放"
	case VehicleInTransit:
		return "调度中"
	default:
		return "未知"
	}
}

// VehicleInfo is the externally visible vehicle state.
type VehicleInfo struct {
	State   VehicleState
	FenceID string // set when State == VehicleParked
}

// TaskKind distinguishes the two dispatch task sources.
type TaskKind int

const (
	RecallTask   TaskKind = iota + 1 // move an outside-returned bike back
	EvacuateTask                     // rebalance a fence that hit its ratio
)

// TaskStatus is the lifecycle state of a dispatch task.
type TaskStatus int

const (
	TaskPending    TaskStatus = iota + 1 // 待认领
	TaskInProgress                       // 处理中
	TaskCompleted                        // 已完成
)

func (s TaskStatus) String() string {
	switch s {
	case TaskPending:
		return "待认领"
	case TaskInProgress:
		return "处理中"
	case TaskCompleted:
		return "已完成"
	default:
		return "未知"
	}
}

// ClaimRecord is one claim attempt on a task. Expired claims are kept.
type ClaimRecord struct {
	DispatcherID string
	ClaimedAt    int64
	Expired      bool // claim timed out and the task fell back to pending
}

// Task is a dispatch task. For RecallTask, VehicleID identifies the bike.
// For EvacuateTask, MoveCount bikes are taken from SourceFenceID.
type Task struct {
	ID            string
	Kind          TaskKind
	Status        TaskStatus
	SourceFenceID string
	DestFenceID   string // planned destination; the actual drop point may differ
	VehicleID     string
	MoveCount     int
	CreatedAt     int64
	Claims        []ClaimRecord
}

// LedgerKind classifies a money event.
type LedgerKind int

const (
	LedgerOutsideFee LedgerKind = iota + 1 // 区外调度费
	LedgerReward                           // 奖励额度
)

// LedgerEntry is one immutable money event.
type LedgerEntry struct {
	Seq       int
	Time      int64
	UserID    string
	VehicleID string
	FenceID   string
	Kind      LedgerKind
	Amount    int64
}

// Attribution is the result of locating a point.
type Attribution struct {
	Type    FenceType // Outside when the point is in no fence
	FenceID string
}

// ReturnResult reports the outcome of an accepted return.
type ReturnResult struct {
	Outside bool     // returned outside every fence
	FenceID string   // fence the vehicle was attributed to ("" when outside)
	Fee     int64    // outside dispatch fee charged
	Reward  int64    // reward granted
	TaskIDs []string // dispatch tasks created by this return
}

// CompleteResult reports the outcome of an accepted task completion.
type CompleteResult struct {
	Moved   int      // vehicles actually moved
	FenceID string   // fence the vehicles were dropped into
	TaskIDs []string // evacuation tasks triggered by the drop
}

// rewardKey scopes the daily reward limit: user x reward fence x day.
type rewardKey struct {
	userID  string
	fenceID string
	day     string
}
