package shophours

// Interval 是周内左闭右开区间，单位为相对周起点的秒偏移。
// 0 <= Start,End <= WeekSec；Start < End 为普通区间，Start > End 为跨周尾回绕区间。
type Interval struct {
	Start int64
	End   int64
}

// OrderKind 区分即时单与预约单。
type OrderKind int

const (
	KindInstant OrderKind = iota
	KindReservation
)

// OrderStatus 为订单生命周期状态。
type OrderStatus int

const (
	OrderAccepted OrderStatus = iota
	OrderStarted
	OrderCompleted
	OrderCancelled
)

// ResponsibleParty 标识取消责任方。
type ResponsibleParty int

const (
	ResponsibleMerchant ResponsibleParty = iota
	ResponsiblePlatform
)

// Order 是对外可查询的订单快照。
type Order struct {
	ID           int64
	Kind         OrderKind
	AcceptedAt   int64
	PromisedAt   int64
	Status       OrderStatus
	CancelledBy  ResponsibleParty
	CancelledAt  int64
	CancelReason ErrorCode
}

// ClosureInfo 是临时歇业记录的对外快照。
type ClosureInfo struct {
	Start      int64
	Duration   int64
	ActualEnd  int64
	EndedEarly bool
}
