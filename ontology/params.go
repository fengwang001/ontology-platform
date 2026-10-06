package ontology

import "sort"

// ContractParams 为某一生效月起适用的产消者合同参数。所有电量/功率为整数。
type ContractParams struct {
	ContractExportWatts  int64 // 合同上网功率上限（瓦）
	MonthlyCreditableKWh int64 // 月度可计入上网电量上限（度）
	CreditValidMonths    int64 // 额度有效月数（>=0；0 表示当月加 0，即下一月初到期）
}

// Prices 为某一生效月起适用的单价（整数，单位自定义）。
type Prices struct {
	ImportPrice int64 // 下网电价（每度）
	ExportPrice int64 // 余电单价（每度）
}

// paramHistory 是按生效月排序的阶梯参数表。
type paramHistory[T any] struct {
	from   []MonthKey
	values []T
}

// init 记录月份 0 起生效的初始值。
func (h *paramHistory[T]) init(v T) {
	h.from = []MonthKey{0}
	h.values = []T{v}
}

// set 令自 from（含）起的值为 v；from 必须大于已有最大生效月（阶梯单调追加）。
func (h *paramHistory[T]) set(from MonthKey, v T) {
	i := sort.Search(len(h.from), func(i int) bool { return h.from[i] >= from })
	if i < len(h.from) && h.from[i] == from {
		h.values[i] = v
		return
	}
	h.from = append(h.from, 0)
	h.values = append(h.values, v)
	copy(h.from[i+1:], h.from[i:])
	copy(h.values[i+1:], h.values[i:])
	h.from[i] = from
	h.values[i] = v
}

func (h *paramHistory[T]) at(m MonthKey) T {
	i := sort.Search(len(h.from), func(i int) bool { return h.from[i] > m })
	return h.values[i-1]
}

// lastFrom 返回当前最新阶梯的生效月。
func (h *paramHistory[T]) lastFrom() MonthKey { return h.from[len(h.from)-1] }

func validateContract(p ContractParams) error {
	if p.ContractExportWatts < 0 || p.MonthlyCreditableKWh < 0 || p.CreditValidMonths < 0 {
		return illegal("contract parameters must be non-negative")
	}
	return nil
}

func validatePrices(pr Prices) error {
	if pr.ImportPrice < 0 || pr.ExportPrice < 0 {
		return illegal("prices must be non-negative")
	}
	return nil
}
