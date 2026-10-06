package ontology

import "time"

import "sort"

// Readings 是一个间隔的双向电表读数（度，非负整数）。
type Readings struct {
	ImportKWh int64 // 下网电量
	ExportKWh int64 // 上网电量（原始值，剔除在汇总时判定）
}

// MonthlyResult 是一个月的结算结果（封账月为最终值，未封账月为当前预览值）。
type MonthlyResult struct {
	Month             MonthKey
	Sealed            bool
	RawImport         int64 // 当月下网电量
	RawExport         int64 // 原始上网电量
	RejectedExport    int64 // 超功率剔除电量（不抵扣不付款）
	CreditableExport  int64 // 计入抵扣的上网电量（不超月度上限）
	SelfOffset        int64 // 当月可计入上网直接抵扣当月下网的电量
	NetExportCredited int64 // 净上网存为额度的电量
	PriorCreditUsed   int64 // 历史额度抵扣当月净下网的电量
	BilledImportKWh   int64 // 按下网电价计费的下网电量
	ImportBill        int64 // 下网电费 = BilledImport * 当月下网电价
	AboveCapPaidKWh   int64 // 超过月度可计入上限而按余电单价付款的电量
	ExpiredCreditKWh  int64 // 本月到期付款的历史额度电量（仅封账月可能非零）
	ExportPayment     int64 // 余电付款 = (AboveCapPaid + ExpiredCredit) * 当月余电单价
	LiveCreditBalance int64 // 截至该月（封账后/预览时）的未到期额度余额
}

// monthComputation 是不依赖封账状态的纯计算：输入月内读数、适用参数/价格、
// 以及历史额度消费回调，输出当月结算明细。
// useCreditsFunc 按顺序消费可用历史额度（只允许到期月晚于当前月的额度）。
type useCreditsFunc func(want int64) int64

func computeMonth(
	m MonthKey,
	readings map[time.Time]Readings,
	g *TimeGrid,
	p ContractParams,
	pr Prices,
	usePrior useCreditsFunc,
) MonthlyResult {
	res := MonthlyResult{Month: m}
	// 固定起点顺序，保证重放确定性。
	starts := make([]time.Time, 0, len(readings))
	for t := range readings {
		starts = append(starts, t)
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i].Before(starts[j]) })

	// 每个间隔独立判定功率超限。合同功率单位为瓦，间隔时长单位为秒，
	// 允许电量（度=kWh）= floor(watts * seconds / 3,600,000)，取等不剔除。
	allowed := p.ContractExportWatts * g.IntervalSeconds() / 3_600_000
	var allowedTotal int64
	for _, t := range starts {
		r := readings[t]
		res.RawImport += r.ImportKWh
		res.RawExport += r.ExportKWh
		a := r.ExportKWh
		if a > allowed {
			res.RejectedExport += a - allowed
			a = allowed
		}
		allowedTotal += a
	}
	// 月度可计入上限分档：超出上限部分按余电单价付款，不进入抵扣。
	if allowedTotal > p.MonthlyCreditableKWh {
		res.AboveCapPaidKWh = allowedTotal - p.MonthlyCreditableKWh
		res.CreditableExport = p.MonthlyCreditableKWh
	} else {
		res.CreditableExport = allowedTotal
	}
	// 先逐度抵扣当月下网。
	if res.CreditableExport >= res.RawImport {
		res.SelfOffset = res.RawImport
		res.NetExportCredited = res.CreditableExport - res.RawImport
	} else {
		res.SelfOffset = res.CreditableExport
		netImport := res.RawImport - res.CreditableExport
		res.PriorCreditUsed = usePrior(netImport)
		res.BilledImportKWh = netImport - res.PriorCreditUsed
	}
	res.ImportBill = res.BilledImportKWh * pr.ImportPrice
	res.ExportPayment = res.AboveCapPaidKWh * pr.ExportPrice // 到期额度付款由封账流程追加
	return res
}
