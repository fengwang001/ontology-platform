package fencing

// orderState 是订单生命周期状态。
type orderState int

const (
	stPending orderState = iota
	stAccepted
	stDelivered
	stCancelledShrink // 接单时因收缩不可达而自动取消（终态）
)

func (st orderState) String() string {
	switch st {
	case stPending:
		return "pending"
	case stAccepted:
		return "accepted"
	case stDelivered:
		return "delivered"
	case stCancelledShrink:
		return "cancelled_by_shrink"
	}
	return "unknown"
}

// order 记录订单；已接单后不再受任何收缩影响。
type order struct {
	id         string
	merchantID string
	cell       string
	state      orderState
	changed    bool // 是否已用过唯一一次改址
}
