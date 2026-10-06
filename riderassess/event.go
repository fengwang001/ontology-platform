package riderassess

// 事件类型。
const (
	TypeLateDelivery = "late_delivery"
	TypeComplaint    = "complaint"
	TypeRefusal      = "refusal"
	TypeFaultCancel  = "fault_cancel"
)

// Event 是一条扣分事件的公开视图。
type Event struct {
	ID         int64
	Rider      string
	At         int64
	Type       string
	RootID     string // 空串表示无根因标识，独立计扣分
	Revoked    bool
	IsEarliest bool   // 是否为簇内最早（计扣分）事件
	AppealID   int64  // 0 表示无申诉
}

// eventStore 负责事件登记、成簇、撤销。
// 具体状态在实现阶段填充。
type eventStore struct{}

func newEventStore() *eventStore { return &eventStore{} }
