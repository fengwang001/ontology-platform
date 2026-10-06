package rental

// State 是预订的生命周期状态；保留的"已失效"不是独立状态，
// 由 now > ExpiresAt 派生，避免引入额外迁移动作。
type State int

const (
	StateHold      State = iota // 保留中（未付款）
	StateConfirmed              // 已确认（已付款）
	StateCancelled              // 已取消
)

// Booking 表示一笔预订，占用 [Checkin, Checkout) 的各夜：
// 入住日当天开始占用，退房日当天不占用。
type Booking struct {
	ID        int
	ListingID string
	Checkin   int
	Checkout  int
	State     State
	ExpiresAt int  // StateHold 下有效；now <= ExpiresAt 仍可付款
	Modified  bool // 是否已修改过日期（仅允许一次）
	NightDiff int  // 修改成功的夜数差，差价只记录不结算
}

// active 判断预订在时刻 now 是否仍占用日历。
func (b *Booking) active(now int) bool {
	switch b.State {
	case StateConfirmed:
		return true
	case StateHold:
		return now <= b.ExpiresAt
	default:
		return false
	}
}

// Refund 为取消退款档位。
type Refund int

const (
	RefundNone Refund = iota // 不退
	RefundHalf               // 退一半
	RefundFull               // 全额退
)

// refundTier 按取消日距入住日的天数分档；边界取等归较宽松的一档。
func refundTier(daysBeforeCheckin, p, q int) Refund {
	if daysBeforeCheckin >= p {
		return RefundFull
	}
	if daysBeforeCheckin >= q {
		return RefundHalf
	}
	return RefundNone
}
