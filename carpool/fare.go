package carpool

// ceilDiv 计算 a/b 的向上取整（a, b 非负，b > 0）。
func ceilDiv(a, b int64) int64 {
	return (a + b - 1) / b
}

// soloFare 返回订单的独行费用：人数 × 里程 × 单价。
func soloFare(o *Order, unitPrice int64) int64 {
	return int64(o.Persons) * (o.Dropoff - o.Pickup) * unitPrice
}

// payable 返回订单最终应付：分摊金额受独行费用与锁价上限双重约束。
func payable(raw, solo, cap int64) int64 {
	v := raw
	if v > solo {
		v = solo
	}
	if v > cap {
		v = cap
	}
	return v
}

// computeRawFares 计算一组在途订单各自的分摊金额（未受上限约束）。
//
// 行程被所有上下车点切成若干段，每段基础费用 = 里程 × 单价，
// 由该段上所有乘客按人数等分，每人金额向上取整，
// 取整差额由该段内下单最早的乘客承担，使每段分摊之和恰好等于基础费用。
// ctr 非空时按（段 × 订单）迭代次数递增，用于查询开销的可验证证明。
func computeRawFares(orders []*Order, unitPrice int64, ctr *int64) map[string]int64 {
	raw := make(map[string]int64, len(orders))
	locs := stopLocations(orders)
	for i := 0; i+1 < len(locs); i++ {
		base := (locs[i+1] - locs[i]) * unitPrice
		var total int64
		var earliest *Order
		for _, o := range orders {
			if ctr != nil {
				*ctr++
			}
			if o.Pickup <= locs[i] && o.Dropoff >= locs[i+1] {
				total += int64(o.Persons)
				if earliest == nil || o.Seq < earliest.Seq {
					earliest = o
				}
			}
		}
		if total == 0 {
			continue
		}
		per := ceilDiv(base, total)
		diff := per*total - base
		for _, o := range orders {
			if o.Pickup <= locs[i] && o.Dropoff >= locs[i+1] {
				share := int64(o.Persons) * per
				if o == earliest {
					share -= diff
				}
				raw[o.ID] += share
			}
		}
	}
	return raw
}
