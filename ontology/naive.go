package ontology

import "sort"

// naivePolicy 是朴素模型自己使用的保单快照，与引擎内部结构解耦。
type naivePolicy struct {
	id     string
	kind   Clause
	ded    int64
	per    int64
	left   int64
	from   int
	to     int
	indep  int64
	weight int64
}

func naiveInd(ded, per, left, amount int64) int64 {
	pay := amount - ded
	if pay < 0 {
		pay = 0
	}
	lim := per
	if left < lim {
		lim = left
	}
	if pay > lim {
		pay = lim
	}
	return pay
}

// naiveCappedDivide 是朴素模型自己的带封顶按比例分配：
// 每轮把已达 cap 的保单剔除，剩余保单重算分母从头分摊；
// 最后一轮用大余数法，尾差给 cap 最大、编号最小者。
func naiveCappedDivide(ps []*naivePolicy, money int64) map[string]int64 {
	out := map[string]int64{}
	got := map[string]int64{}
	alive := map[string]bool{}
	var totalWeight int64
	for _, p := range ps {
		alive[p.id] = true
		if p.weight <= 0 {
			alive[p.id] = false // 独立责任额为 0：不入分母，也不参与尾差
			continue
		}
		totalWeight += p.weight
	}
	for {
		alivePositive := 0
		for _, p := range ps {
			if alive[p.id] && p.weight > 0 {
				alivePositive++
			}
		}
		if money <= 0 || alivePositive == 0 {
			break
		}
		var baseSum int64
		floor := map[string]int64{}
		hitAny := false
		for _, p := range ps {
			if !alive[p.id] {
				continue
			}
			q := (money * p.weight) / totalWeight
			free := p.indep - got[p.id]
			if q >= free {
				got[p.id] += free
				money -= free
				alive[p.id] = false
				totalWeight -= p.weight
				hitAny = true
				continue
			}
			floor[p.id] = q
			baseSum += q
		}
		if hitAny {
			continue
		}
		rest := money - baseSum
		for id, q := range floor {
			got[id] += q
		}
		ids := make([]string, 0, len(floor))
		for id := range floor {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for k := int64(0); k < rest; k++ {
			choose := ""
			for _, id := range ids {
				var p *naivePolicy
				for _, q := range ps {
					if q.id == id {
						p = q
					}
				}
				if p == nil {
					continue
				}
				if got[id] >= p.indep {
					continue
				}
				if choose == "" {
					choose = id
					continue
				}
				var cp *naivePolicy
				for _, q := range ps {
					if q.id == choose {
						cp = q
					}
				}
				if p.indep > cp.indep || (p.indep == cp.indep && id < choose) {
					choose = id
				}
			}
			if choose == "" {
				break
			}
			got[choose]++
		}
		break
	}
	for id, v := range got {
		out[id] = v
	}
	return out
}

// naiveApportion 为按同一规则独立写成的朴素模型，仅依赖输入快照，供随机对照使用。
func naiveApportion(policies []*policyState, day int, amount int64) ApportionResult {
	res := ApportionResult{
		Stage1: StageResult{Shares: map[string]int64{}},
		Stage2: StageResult{Shares: map[string]int64{}},
		Shares: map[string]int64{},
	}
	var primary, excess []*naivePolicy
	independentExists := false
	var cap1 int64
	for _, st := range policies {
		p := st.spec
		if day < p.CoverFrom || day >= p.CoverTo || st.remains <= 0 {
			continue
		}
		np := &naivePolicy{
			id: p.PolicyID, kind: p.Clause, ded: p.Deductible, per: p.PerLossLimit,
			left: st.remains, from: p.CoverFrom, to: p.CoverTo,
		}
		np.indep = naiveInd(np.ded, np.per, np.left, amount)
		if p.Clause == ClauseExcess {
			np.weight = np.indep
			excess = append(excess, np)
			continue
		}
		if p.Clause == ClauseIndependentLiability {
			independentExists = true
		}
		if independentExists {
			np.weight = np.indep
		} else {
			np.weight = np.per
			if np.left < np.weight {
				np.weight = np.left
			}
		}
		primary = append(primary, np)
		cap1 += np.indep
	}
	if len(primary)+len(excess) == 0 {
		res.NoPayer = true
		return res
	}

	// 一旦存在独立责任型，所有非超额保单都按独立责任额为权重（含已先入列者）。
	if independentExists {
		for _, np := range primary {
			np.weight = np.indep
		}
	}
	goal1 := amount
	if cap1 < goal1 {
		goal1 = cap1
	}
	res.Stage1.Shares = naiveCappedDivide(primary, goal1)
	if independentExists {
		res.Stage1.Mode = "独立责任比例"
	} else {
		res.Stage1.Mode = "限额比例"
	}
	var paid int64
	for _, v := range res.Stage1.Shares {
		paid += v
	}

	if left := amount - paid; left > 0 && len(excess) > 0 {
		var cap2 int64
		for _, np := range excess {
			cap2 += np.indep
		}
		goal2 := left
		if cap2 < goal2 {
			goal2 = cap2
		}
		res.Stage2.Mode = "独立责任比例(超额)"
		res.Stage2.Shares = naiveCappedDivide(excess, goal2)
	}
	for id, v := range res.Stage1.Shares {
		res.Shares[id] = v
	}
	for id, v := range res.Stage2.Shares {
		res.Shares[id] += v
	}
	for _, v := range res.Shares {
		res.Paid += v
	}
	return res
}
