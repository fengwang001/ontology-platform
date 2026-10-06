package ffm

// 定级周期与等级引擎：纯函数 + account 上的结算过程。

// tierForQM 按周期累计定级里程定级（恰等于阈值即达到）。
func tierForQM(qm int64, c Config) int {
	tier := 0
	for i := 0; i < 3; i++ {
		if qm >= c.Thresholds[i] {
			tier = i + 1
		}
	}
	return tier
}

// settleAfterLevel 返回周期结束时的等级：
// 以该周期累计 qm 重新定级，但结论既不高于当前等级（周期内单调不降，
// 新周期不允许“带着旧周期里程续升”），也不低于当前等级减一级（一次最多降一级）。
func settleAfterLevel(currentTier int, qm int64, c Config) int {
	desired := tierForQM(qm, c)
	floor := currentTier - 1
	if floor < 0 {
		floor = 0
	}
	if desired < floor {
		desired = floor
	}
	if desired > currentTier {
		desired = currentTier
	}
	return desired
}

// settleTo 推进账户定级周期：结算 (a.periodIdx, idxNow] 中所有完整周期。
// 每个结束周期按其最终累计定级里程重定级，但每次最多降一级。
// 结算开销为 O(1)：紧邻周期用真实累计判定一次，其余被跳过周期累计均为 0，
// 而每个空周期至多让等级下降 1 级，故 k 个空周期后等级恒为 max(0, tier-k)。
// 全程只读写标量字段，与历史入账/扣回/兑换记录数量无关。
//
// 关键不变量：只有“上一个当前周期”在结束时持有尚未结算的定级里程
// （a.periodQM）；更早的周期在结算当时已把里程计入判定并随即丢弃，
// 之后对它们的补登/扣回只改历史航段记录，不再触及等级。
func (a *account) settleTo(idxNow int64) {
	if idxNow <= a.periodIdx {
		return
	}
	// 1) 结算紧邻当前周期的那一个完整周期（它的累计里程还保留在 a.periodQM）。
	a.tier = settleAfterLevel(a.tier, a.periodQM, *a.cfg)
	a.periodIdx++
	// 2) 其余被跳过周期累计均为 0：每周期最多降一级，直接取闭式结果。
	emptyPeriods := idxNow - a.periodIdx
	if int64(a.tier) <= emptyPeriods {
		a.tier = 0
	} else {
		a.tier -= int(emptyPeriods)
	}
	a.periodIdx = idxNow
	// 3) 进入新的当前周期，累计从零开始。
	a.periodQM = 0
}

// projectedTier 计算在不修改状态的前提下，时刻 now 查询应得到的等级；
// 返回（等级, 查询时刻所属周期序号）。
func (a *account) projectedTier(idxNow int64) int {
	if idxNow <= a.periodIdx {
		return a.tier
	}
	tier := settleAfterLevel(a.tier, a.periodQM, *a.cfg)
	emptyPeriods := idxNow - a.periodIdx - 1
	if int64(tier) <= emptyPeriods {
		return 0
	}
	return tier - int(emptyPeriods)
}
