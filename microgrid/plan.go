package microgrid

import (
	"maps"
	"slices"
)

// applyAction 返回执行 a 后的荷电；充电按折损比例折算后向下取整。
func applyAction(cfg Config, soc int, a PlanAction) int {
	switch a.Action {
	case ActionCharge:
		return soc + a.Amount*(1000-cfg.ChargeLossPermille)/1000
	case ActionDischarge:
		return soc - a.Amount
	}
	return soc
}

// signedAmount 将动作折算为带符号电量（充电为正、放电为负），用于偏差度量。
func signedAmount(a PlanAction) int {
	switch a.Action {
	case ActionCharge:
		return a.Amount
	case ActionDischarge:
		return -a.Amount
	}
	return 0
}

// validateAction 校验单个计划动作的参数合法性，返回空串表示合法。
func validateAction(cfg Config, a PlanAction) string {
	switch {
	case !a.Action.valid():
		return "未知动作"
	case a.Amount < 0:
		return "电量为负"
	case a.Action == ActionIdle && a.Amount != 0:
		return "闲置时隙电量必须为零"
	case a.Action == ActionCharge && a.Amount > cfg.MaxChargePerSlot:
		return "充电电量超过单时隙最大值"
	case a.Action == ActionDischarge && a.Amount > cfg.MaxDischargePerSlot:
		return "放电电量超过单时隙最大值"
	}
	return ""
}

// planBook 保存已接受且未执行的逐时隙计划。
type planBook struct {
	acts map[int]PlanAction
}

func newPlanBook() *planBook {
	return &planBook{acts: make(map[int]PlanAction)}
}

// slots 按升序返回全部已接受时隙，保证推演顺序确定。
func (b *planBook) slots() []int {
	return slices.Sorted(maps.Keys(b.acts))
}

// mergedWith 返回用新计划覆盖 [start, start+len(acts)) 后的完整视图，
// 范围外已接受的旧时隙保留。
func (b *planBook) mergedWith(start int, acts []PlanAction) map[int]PlanAction {
	m := maps.Clone(b.acts)
	end := start + len(acts) - 1
	for s := range m {
		if s >= start && s <= end {
			delete(m, s)
		}
	}
	for i, a := range acts {
		m[start+i] = a
	}
	return m
}

// replaceRange 用新计划替换 [start, end] 范围内的旧计划。
func (b *planBook) replaceRange(start, end int, acts []PlanAction) {
	for s := start; s <= end; s++ {
		delete(b.acts, s)
	}
	for i, a := range acts {
		b.acts[start+i] = a
	}
}
