// Package kitchen 实现商家出餐节奏与压单控制系统。
//
// 模块按职责拆分：
//   - errors.go：可程序化区分的错误类型；
//   - estimate.go：制作队列与开工推定；
//   - pressure.go：压单状态机与事件记录；
//   - kitchen.go：订单生命周期、准入判定与并发协调；
//   - reference.go：仅测试使用的逐秒朴素对照模型。
package kitchen

// Config 是构造一个商家厨房的全部参数。
//
// 字段单位均为整数秒：
//   - Parallelism：并行制作上限（制作位数量），必须为正；
//   - EnterThreshold：压单进入阈值，必须为正；
//   - ExitThreshold：压单退出阈值，必须严格小于进入阈值；
//   - RejectThreshold：爆单阈值，必须严格大于进入阈值；
//   - ReservationLead：预约单提前量，必须非负。
type Config struct {
	Parallelism     int
	EnterThreshold  int64
	ExitThreshold   int64
	RejectThreshold int64
	ReservationLead int64
}

// OrderKind 区分即时单与预约单。
type OrderKind int

const (
	// Immediate 表示即时单：按接单次序排队。
	Immediate OrderKind = iota
	// Reservation 表示预约单：携带目标取货时刻，目标开工时刻优先。
	Reservation
)

// OrderRequest 是一笔记入操作携带的订单信息。
type OrderRequest struct {
	// ID 为订单唯一标识，不允许为空串。
	ID string
	// Kind 为订单类型。
	Kind OrderKind
	// Duration 为声明的制作时长，必须为正整数秒。
	Duration int64
	// TargetPickup 为预约单目标取货时刻，仅对预约单有效；
	// 目标开工时刻 = TargetPickup - Duration。
	TargetPickup int64
}

// OrderStatus 为订单生命周期状态。
type OrderStatus int

const (
	// StatusWaiting：已接单、尚未开工（队列中，或预约单等待目标时刻）。
	StatusWaiting OrderStatus = iota
	// StatusCooking：已开工、尚未报告完成。
	StatusCooking
	// StatusDone：已报告完成。
	StatusDone
	// StatusCancelled：排队阶段被取消（已开工订单不允许取消）。
	StatusCancelled
)

// AdmitResult 是即时单/预约单准入判定的结果。
type AdmitResult struct {
	// Accepted 表示订单是否被接受（暂停、预约过近、爆单时为 false）。
	Accepted bool
	// PromisePickup 为承诺取货时刻，仅接单时有意义：
	// 正常即时单 = 当前时刻 + 制作时长 + 进入阈值；
	// 压单即时单 = 推定完成时刻；
	// 预约单 = 目标取货时刻。
	PromisePickup int64
	// EstimatedStart 为该单的推定开工时刻，仅接单时有意义。
	EstimatedStart int64
	// EstimatedFinish 为推定完成时刻，仅接单时有意义。
	EstimatedFinish int64
	// ExpectedWait 为本次准入判定时该单的预计等待（推定开工-当前时刻）。
	ExpectedWait int64
	// Pressed 为接单后商家是否处于压单状态。
	Pressed bool
}

// OrderInfo 是单笔订单的只读快照。
type OrderInfo struct {
	ID             string
	Kind           OrderKind
	Duration       int64
	TargetPickup   int64
	Status         OrderStatus
	EnqueueSeq     int64 // 接单次序（用于确定性 tie-break）
	StartAt        int64 // 实际开工时刻
	FinishAt       int64 // 实际报告完成时刻
	PromisePickup  int64
	EstimatedStart int64 // 接单当时的推定开工时刻（压单承诺依据）
}

// PressureEvent 是一条压单状态变化记录，进入与退出严格交替。
type PressureEvent struct {
	// Seq 为事件序号，从 1 开始单调递增。
	Seq int
	// At 为导致状态变化的被接受操作时刻。
	At int64
	// Entered 为 true 表示进入压单，false 表示退出压单。
	Entered bool
	// Reason 描述触发来源，便于复现与排查。
	Reason string
	// ExpectedWait 为触发判定时的假想即时单预计等待。
	ExpectedWait int64
}
