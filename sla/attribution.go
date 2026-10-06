package sla

// attribution 计算延误分段归因。

type attributionResult struct {
	merchant int64
	rider    int64
	platform int64
	user     int64
}

func attribute(o *order, extendedPromise int64) attributionResult {
	r := attributionResult{}

	// 商家段：出餐就绪时刻超出（接受时刻 + 出餐容许时长）的部分。
	if over := o.readyAt - (o.acceptedAt + o.merchantPrep); over > 0 {
		r.merchant = over
	}
	// 平台段：派单时刻超出（接受时刻 + 派单容许时长）的部分。
	if over := o.dispatchAt - (o.acceptedAt + o.platformDispatch); over > 0 {
		r.platform = over
	}
	// 骑手段 a：取货时刻超出（出餐就绪时刻 + 取货容许时长）的部分。
	if over := o.pickupAt - (o.readyAt + o.riderPickup); over > 0 {
		r.rider += over
	}
	// 骑手段 b：送达时刻超出（取货时刻 + 剩余承诺余量）的部分。
	// 剩余承诺余量 = 延展后承诺时刻 - 取货时刻，不为负。
	remaining := extendedPromise - o.pickupAt
	if remaining < 0 {
		remaining = 0
	}
	if over := o.deliverAt - (o.pickupAt + remaining); over > 0 {
		r.rider += over
	}
	// 用户段：改址发生在取货之后时，计入固定延展量。
	if o.readdressed && o.readdressAt > o.pickupAt {
		r.user = o.userExt
	}
	return r
}

// chooseParty 按归因量取最大；并列次序为 商家、骑手、平台、用户。
func (r attributionResult) chooseParty() Party {
	best := PartyMerchant
	bestVal := r.merchant
	if r.rider > bestVal {
		best, bestVal = PartyRider, r.rider
	}
	if r.platform > bestVal {
		best, bestVal = PartyPlatform, r.platform
	}
	if r.user > bestVal {
		best = PartyUser
	}
	if bestVal == 0 {
		// 全部归因量为零而延误为正：责任方为平台。
		return PartyPlatform
	}
	return best
}
