package freight

import "sort"

// ceilDiv 向上取整除法，a >= 0, b > 0。
func ceilDiv(a, b int64) int64 {
	return (a + b - 1) / b
}

// roundUpTo 将 w 向上取整到 unit 的整数倍。
func roundUpTo(w, unit int64) int64 {
	return ceilDiv(w, unit) * unit
}

// maxSide 返回三边中的最大边。
func maxSide(dims [3]int64) int64 {
	m := dims[0]
	if dims[1] > m {
		m = dims[1]
	}
	if dims[2] > m {
		m = dims[2]
	}
	return m
}

// billingWeight 计费重量：实际重量与体积重量取大，再向上取整到计重单位整数倍。
func billingWeight(c *Contract, actualWeight, volume int64) int64 {
	volumetric := ceilDiv(volume, c.VolumeDivisor)
	w := actualWeight
	if volumetric > w {
		w = volumetric
	}
	return roundUpTo(w, c.BillingUnit)
}

// baseFee 累进阶梯基础运费：计费重量落在各档内的部分分别按各档单价计价。
// 利用预计算的前缀和，复杂度 O(log 档数)。
func baseFee(pc *pricedContract, bw int64) int64 {
	tiers := pc.contract.Tiers
	// 找到第一档 Upper >= bw 的档；bw 必落在最后一档终点之内或之上。
	i := sort.Search(len(tiers), func(i int) bool { return tiers[i].Upper >= bw })
	if i >= len(tiers) {
		// 超出最高档：按完整阶梯计价后，超出部分按最高档单价计。
		last := tiers[len(tiers)-1]
		over := (bw - last.Upper) / pc.contract.BillingUnit
		return pc.tierPrefix[len(tiers)] + over*last.PricePerUnit
	}
	inTier := (bw - tiers[i].Lower) / pc.contract.BillingUnit
	return pc.tierPrefix[i] + inTier*tiers[i].PricePerUnit
}

// outOfRange 判定是否超出承运范围：使用实际重量与最大单边尺寸，先于计费。
func outOfRange(c *Contract, actualWeight int64, dims [3]int64) bool {
	return actualWeight > c.MaxWeight || maxSide(dims) > c.MaxDim
}

// price 按合同对运单计费，返回可复现的费用明细。
// 调用前须保证参数合法且未超出承运范围。
func price(pc *pricedContract, actualWeight, volume int64, dims [3]int64, dest string) FeeBreakdown {
	c := &pc.contract
	bw := billingWeight(c, actualWeight, volume)

	base := baseFee(pc, bw)
	afterMin := base
	if afterMin < c.MinCharge {
		afterMin = c.MinCharge
	}
	fuel := ceilDiv(afterMin*c.FuelPermille, 1000)

	var remote int64
	if pc.remote[dest] {
		remote = c.RemoteFee
	}
	var dimExcess int64
	if maxSide(dims) > c.DimThreshold {
		dimExcess = c.DimExcessFee
	}
	var weightExcess int64
	if bw > c.WeightThreshold {
		weightExcess = c.WeightExcessFee
	}

	return FeeBreakdown{
		ContractID:      c.ID,
		BillingWeight:   bw,
		BaseFee:         base,
		BaseAfterMin:    afterMin,
		FuelFee:         fuel,
		RemoteFee:       remote,
		DimExcessFee:    dimExcess,
		WeightExcessFee: weightExcess,
		Total:           afterMin + fuel + remote + dimExcess + weightExcess,
	}
}
