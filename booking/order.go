package booking

// order 是一笔预约的生命周期状态。
type order struct {
	id        string
	region    string
	slotStart int64
	postponed bool
	origSlot  int64
	canceled  bool
	delivered bool
	// cancelAfterRelease 记录取消发生在释放之后。
	cancelAfterRelease bool
}

// releaseTime 为释放时刻：时段起点 - 派单提前量。
func (o *order) releaseTime(cfg Config) int64 { return o.slotStart - cfg.DispatchLead }

// isReleasedAt 是时刻的纯函数：now >= releaseTime 即视为已释放
// （恰等即释放），无需任何中间操作触碰该预约，因此判定开销为 O(1)，
// 与预约总数无关。
func (o *order) isReleasedAt(now int64, cfg Config) bool {
	return !o.canceled && !o.delivered && now >= o.releaseTime(cfg)
}

// active 表示当前是否仍占名额：未取消且未送达。释放派单后仍占名额。
func (o *order) active() bool { return !o.canceled && !o.delivered }

func (o *order) snapshot(now int64, cfg Config) OrderInfo {
	return OrderInfo{
		OrderID:              o.id,
		Region:               o.region,
		SlotStart:            o.slotStart,
		Postponed:            o.postponed,
		OrigSlot:             o.origSlot,
		Released:             o.isReleasedAt(now, cfg),
		Canceled:             o.canceled,
		Delivered:            o.delivered,
		CanceledAfterRelease: o.cancelAfterRelease,
	}
}

// PlaceRequest 为下单请求。
type PlaceRequest struct {
	OrderID        string
	Region         string
	SlotStart      int64
	AcceptPostpone bool
}

// PlaceResult 为下单落位结果。
type PlaceResult struct {
	SlotStart int64
	Postponed bool
	OrigSlot  int64
}

// OrderInfo 为预约查询结果。
type OrderInfo struct {
	OrderID              string
	Region               string
	SlotStart            int64
	Postponed            bool
	OrigSlot             int64
	Released             bool
	Canceled             bool
	Delivered            bool
	CanceledAfterRelease bool
}
