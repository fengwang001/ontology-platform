package fulfillment

// Party 延误责任方。
type Party int

const (
	Merchant Party = iota
	Rider
	Platform
	User
)

func (p Party) String() string {
	switch p {
	case Merchant:
		return "merchant"
	case Rider:
		return "rider"
	case Platform:
		return "platform"
	case User:
		return "user"
	}
	return "unknown"
}

// attribution 各责任方的归因量，只计入实际超出容许时长的部分。
type attribution struct {
	merchant int64
	rider    int64
	platform int64
	user     int64
}

// attribute 按阶段把延误分段归因。
func attribute(o *order) attribution {
	var a attribution
	// 商家段：出餐就绪超出 接受时刻+出餐容许 的部分。
	a.merchant = max(0, o.mealReadyTs-(o.acceptTs+o.params.PrepAllowance))
	// 平台段：派单超出 接受时刻+派单容许 的部分。
	a.platform = max(0, o.dispatchTs-(o.acceptTs+o.params.DispatchAllowance))
	// 骑手段一：取货超出 出餐就绪+取货容许 的部分。
	a.rider = max(0, o.pickupTs-(o.mealReadyTs+o.params.PickupAllowance))
	// 骑手段二：送达超出 取货时刻+剩余承诺余量 的部分；余量不为负。
	remaining := max(0, o.promise()-o.pickupTs)
	a.rider += max(0, o.deliverTs-(o.pickupTs+remaining))
	// 用户段：改址发生在取货之后时的固定延展量。
	if o.addrChanged && o.addrChangeTs > o.pickupTs {
		a.user = o.params.AddressExtension
	}
	return a
}

// responsible 取归因量最大的一方；并列按 商家、骑手、平台、用户 取靠前；
// 全部为零（调用方保证延误为正）时归平台。
func (a attribution) responsible() Party {
	best, bestVal := Merchant, a.merchant
	if a.rider > bestVal {
		best, bestVal = Rider, a.rider
	}
	if a.platform > bestVal {
		best, bestVal = Platform, a.platform
	}
	if a.user > bestVal {
		best, bestVal = User, a.user
	}
	if bestVal == 0 {
		return Platform
	}
	return best
}
