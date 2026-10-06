package settlement

// Params 是某产消者在某个生效月起适用的参数。
// 电量单位 Wh（整数），功率单位 W（整数）；间隔能量上限 = 功率上限 × 间隔时长。
type Params struct {
	ContractPowerW     int // 合同上网功率上限（W）
	MonthlyCreditableW int // 月度可计入上网电量上限（Wh）
	CreditValidMonths  int // 新存入额度的有效月数（可计为 0，即当月抵扣后立即到期付款）
}

// Prices 是某生效月起适用的电价；金额 = 电量（Wh） × 单价（分/Wh），全程整数。
type Prices struct {
	ImportPricePerWh  int // 下网电价
	SurplusPricePerWh int // 余电单价
}

// Reading 是一个计量间隔的双向电表登记值，两项均为该间隔内电量（Wh，非负）。
type Reading struct {
	Start    Interval // 间隔起点（必须对齐计量网格，属于哪个月由其 UTC 时刻决定）
	ImportWh int      // 下网电量
	ExportWh int      // 上网电量（未经超限剔除前的原始值）
}

// CreditLot 是一笔净余上网形成的可抵扣额度；金额不在这里，到期时按到期月余电单价付款。
type CreditLot struct {
	DepositMonth Month
	ExpireMonth  Month
	RemainingWh  int
}

// MonthResult 是一个月的完整结算结果，真实封账与未封账试算返回同一结构。
type MonthResult struct {
	Month              Month
	ImportWh           int // 当月下网电量（原始合计）
	ExportRawWh        int // 当月上网电量（原始合计）
	CurtailedWh        int // 上网功率超限被剔除的电量，不抵扣也不付款
	AboveCapWh         int // 未超间隔功率但超过月度可计入上限的电量：按余电单价付款
	CreditableWh       int // 当月可计入上网电量：剔除后且不超月度上限
	SelfOffsetWh       int // 当月可计入上网直接抵扣当月下网的电量
	DepositedWh        int // 抵扣当月下网后净余、存入额度的电量
	HistoryUsedWh      int // 被历史额度抵扣的下网电量
	BilledWh           int // 不足抵扣、按下网电价计费的电量
	ExpiredPaidWh      int // 本月到期并按余电单价付款清零的额度电量
	NewCreditRemaining int // 本月新存额度的笔（封账后进入活动堆；试算时不落库）
	ImportBill         int // 下网电费：BilledWh × 下网电价
	SurplusPayment     int // 余电付款：(AboveCapWh + ExpiredPaidWh) × 余电单价
}

// schedule 是「自某生效月起取值」的阶梯表：按生效月升序，查询时取最后一个 <=m 的项。
type scheduleEntry[T any] struct {
	from  Month
	value T
}

func valueAt[T any](s []scheduleEntry[T], m Month) (T, bool) {
	var zero T
	lo, hi := 0, len(s)
	for lo < hi {
		mid := (lo + hi) / 2
		if s[mid].from.index() <= m.index() {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == 0 {
		return zero, false
	}
	return s[lo-1].value, true
}

// upsertSchedule 插入或覆盖自 from 起的取值，保持升序。调用方已做封账类校验。
func upsertSchedule[T any](s []scheduleEntry[T], from Month, v T) []scheduleEntry[T] {
	idx := -1
	for i := range s {
		if s[i].from.equal(from) {
			idx = i
			break
		}
	}
	if idx >= 0 {
		s[idx].value = v
		return s
	}
	s = append(s, scheduleEntry[T]{})
	i := len(s) - 1
	for i > 0 && s[i-1].from.index() > from.index() {
		s[i] = s[i-1]
		i--
	}
	s[i] = scheduleEntry[T]{from: from, value: v}
	return s
}
