package calendar

// BookingStatus 是预订的生命周期状态。
type BookingStatus int

const (
	StatusHold      BookingStatus = iota + 1 // 保留（未付款，占用日历）
	StatusConfirmed                          // 已确认（已付款）
	StatusCancelled                          // 已取消
	StatusExpired                            // 保留过期失效
)

func (s BookingStatus) String() string {
	switch s {
	case StatusHold:
		return "hold"
	case StatusConfirmed:
		return "confirmed"
	case StatusCancelled:
		return "cancelled"
	case StatusExpired:
		return "expired"
	default:
		return "unknown"
	}
}

// Booking 表示一笔预订。占用夜为 [CheckIn, CheckOut)：
// 入住日当天开始占用，退房日当天不占用。
type Booking struct {
	ID        int64
	ListingID string
	CheckIn   int64
	CheckOut  int64
	CreatedAt int64
	ExpiresAt int64 // 保留在 now <= ExpiresAt 时仍可付款
	Status    BookingStatus
	Modified  bool  // 是否已修改过日期（仅允许一次）
	Amount    int64 // 已付款金额（创建保留时的报价，支付不改变它）
	PriceDiff int64 // 修改日期产生的差价，只记录不结算
}

func (b *Booking) nights() int64 { return b.CheckOut - b.CheckIn }

// activeAt 报告该预订在 now 时刻是否占用日历。
func (b *Booking) activeAt(now int64) bool {
	switch b.Status {
	case StatusConfirmed:
		return true
	case StatusHold:
		return now <= b.ExpiresAt
	default:
		return false
	}
}
