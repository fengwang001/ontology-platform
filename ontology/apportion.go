package ontology

import (
	"math/big"
	"sort"
	"strconv"
)

// liability 为一张参与候选保单在损失时刻的视图。
type liability struct {
	policyID string
	clause   Clause
	weight   int64 // 限额比例型分母权重：min(每次损失限额, 年度累计剩余)
	cap      int64 // 独立责任额
}

// StageResult 为一个分摊阶段的结果。
type StageResult struct {
	Mode   string
	Shares map[string]int64
	Reason string // 判定依据，供日志使用
}

// ApportionResult 为一次损失的完整分摊裁定。
type ApportionResult struct {
	Stage1  StageResult
	Stage2  StageResult
	Paid    int64
	NoPayer bool
	Shares  map[string]int64
	Reason  string
}

// independentLiability 计算独立责任额：clamp(loss-deductible, 0, min(perLoss, remains))。
func independentLiability(p Policy, remains, lossAmount int64) int64 {
	v := lossAmount - p.Deductible
	if v < 0 {
		v = 0
	}
	cap := p.PerLossLimit
	if remains < cap {
		cap = remains
	}
	if v > cap {
		v = cap
	}
	return v
}

// floorDiv 精确计算 floor(a*b/d)，避免 int64 乘法溢出与浮点误差。
func floorDiv(a, b, d int64) int64 {
	x := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	x.Quo(x, big.NewInt(d))
	return x.Int64()
}

// allocateStage 在一个阶段内按 weight 比例分摊 target，每张不超过 cap。
// 超过 cap 的份额被截断，余额按同一比例在未达 cap 的保单间继续迭代；
// 向下取整产生的尾差逐分分给 cap 最大、且尚未达 cap 的保单，
// 并列时给保单编号字典序最小者。
func allocateStage(items []liability, target int64) map[string]int64 {
	shares := make(map[string]int64, len(items))
	sorted := make([]liability, len(items))
	copy(sorted, items)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].policyID < sorted[j].policyID })

	capped := make(map[string]bool, len(sorted))
	remaining := target
	for {
		var denom int64
		alivePositive := 0
		for _, it := range sorted {
			if !capped[it.policyID] && it.weight > 0 {
				denom += it.weight
				alivePositive++
			}
		}
		if remaining <= 0 || alivePositive == 0 {
			break
		}

		// 第一遍：探测本轮是否有任何保单达到 headroom。
		newCapped := map[string]bool{}
		for _, it := range sorted {
			if capped[it.policyID] || it.weight <= 0 {
				continue
			}
			headroom := it.cap - shares[it.policyID]
			q := floorDiv(remaining, it.weight, denom)
			if q >= headroom {
				newCapped[it.policyID] = true
			}
		}

		// 第二遍：有人封顶则只截顶，用新分母重新探测；否则落 floor 并处理尾差。
		if len(newCapped) > 0 {
			for id := range newCapped {
				capped[id] = true
			}
			for _, it := range sorted {
				if newCapped[it.policyID] {
					remaining -= it.cap - shares[it.policyID]
					shares[it.policyID] = it.cap
				}
			}
			continue
		}

		floors := make(map[string]int64, len(sorted))
		var assigned int64
		for _, it := range sorted {
			if capped[it.policyID] || it.weight <= 0 {
				continue
			}
			q := floorDiv(remaining, it.weight, denom)
			floors[it.policyID] = q
			shares[it.policyID] = q
			assigned += q
		}
		for pennies := remaining - assigned; pennies > 0; pennies-- {
			pick := -1
			for i := range sorted {
				it := &sorted[i]
				if capped[it.policyID] || shares[it.policyID] >= it.cap {
					continue
				}
				if pick < 0 || it.cap > sorted[pick].cap ||
					(it.cap == sorted[pick].cap && it.policyID < sorted[pick].policyID) {
					pick = i
				}
			}
			if pick < 0 {
				break
			}
			shares[sorted[pick].policyID]++
			assigned++
		}
		break
	}
	return shares
}

func stageItems(states []*policyState, amount int64, useCapWeight bool) ([]liability, int64) {
	items := make([]liability, 0, len(states))
	var sumCap int64
	for _, st := range states {
		cap := independentLiability(st.spec, st.remains, amount)
		w := cap
		if !useCapWeight {
			w = st.spec.PerLossLimit
			if st.remains < w {
				w = st.remains
			}
		}
		items = append(items, liability{
			policyID: st.spec.PolicyID,
			clause:   st.spec.Clause,
			weight:   w,
			cap:      cap,
		})
		sumCap += cap
	}
	return items, sumCap
}

// apportion 执行两阶段分摊。policies 为该被保人区间覆盖损失日、且年度剩余 > 0 的保单快照。
func apportion(policies []*policyState, day int, amount int64) ApportionResult {
	res := ApportionResult{
		Stage1: StageResult{Shares: map[string]int64{}},
		Stage2: StageResult{Shares: map[string]int64{}},
		Shares: map[string]int64{},
	}
	if len(policies) == 0 {
		res.NoPayer = true
		res.Reason = "无承保区间覆盖损失日且年度累计有剩余的保单，结论：无可赔保单"
		return res
	}

	var primary, excess []*policyState
	hasIndependent := false
	for _, st := range policies {
		switch st.spec.Clause {
		case ClauseExcess:
			excess = append(excess, st)
		default:
			primary = append(primary, st)
			if st.spec.Clause == ClauseIndependentLiability {
				hasIndependent = true
			}
		}
	}

	mode := "限额比例"
	if hasIndependent {
		mode = "独立责任比例"
	}
	p1Items, sumCap1 := stageItems(primary, amount, hasIndependent)
	target1 := amount
	if sumCap1 < target1 {
		target1 = sumCap1
	}
	res.Stage1 = StageResult{
		Mode:   mode,
		Shares: allocateStage(p1Items, target1),
		Reason: "阶段一：非超额保单共 " + strconv.Itoa(len(primary)) +
			" 张，" + mode + "分摊，目标额 " + strconv.FormatInt(target1, 10) +
			"（损失 " + strconv.FormatInt(amount, 10) + " 与独立责任额合计 " + strconv.FormatInt(sumCap1, 10) + " 取小）",
	}
	var paid int64
	for _, v := range res.Stage1.Shares {
		paid += v
	}

	rest := amount - paid
	if rest > 0 && len(excess) > 0 {
		p2Items, sumCap2 := stageItems(excess, amount, true)
		target2 := rest
		if sumCap2 < target2 {
			target2 = sumCap2
		}
		res.Stage2 = StageResult{
			Mode:   "独立责任比例(超额)",
			Shares: allocateStage(p2Items, target2),
			Reason: "阶段二：阶段一未赔足，剩余 " + strconv.FormatInt(rest, 10) +
				"，超额保单 " + strconv.Itoa(len(excess)) + " 张按独立责任额比例分摊，目标额 " + strconv.FormatInt(target2, 10),
		}
		for _, v := range res.Stage2.Shares {
			paid += v
		}
	} else if len(excess) > 0 {
		res.Stage2 = StageResult{
			Mode:   "独立责任比例(超额)",
			Shares: map[string]int64{},
			Reason: "阶段二：阶段一已赔足，超额保单不参与",
		}
	}

	for id, v := range res.Stage1.Shares {
		res.Shares[id] = v
	}
	for id, v := range res.Stage2.Shares {
		res.Shares[id] += v
	}
	res.Paid = paid
	res.Reason = res.Stage1.Reason
	if res.Stage2.Reason != "" {
		res.Reason += "；" + res.Stage2.Reason
	}
	return res
}
