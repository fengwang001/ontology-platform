package apportion

import "math/big"

// naiveModel 是对照引擎独立书写的朴素模型：不复用产品代码的任何分摊函数，
// 用 big.Rat 精确计算比例、逐轮封顶，尾差按「IL 最大、编号最小」逐分挑选。
type naivePolicy struct {
	no               string
	deduct, per, rem int64
	start, end       int64
	clause           Clause
}

type naiveModel struct {
	policies []naivePolicy
	order    []string
	deduct   map[string]map[string]int64 // lossNo -> 保单扣减额
	lossAmt  map[string]int64
	lossDay  map[string]int64
}

func (m *naiveModel) il(p naivePolicy, amount int64) int64 {
	pay := amount - p.deduct
	if pay < 0 {
		pay = 0
	}
	if pay > p.per {
		pay = p.per
	}
	if pay > p.rem {
		pay = p.rem
	}
	return pay
}

func floorRat(r *big.Rat) int64 {
	q := new(big.Int).Quo(r.Num(), r.Denom()) // 本模型中金额非负
	return q.Int64()
}

// stage 用精确分数模拟单阶段分摊。
// ilAmount 为整笔损失金额（独立责任额的定义基准），target 为本阶段待分目标，
// 比例按 weights 分 target，但每人不超过其独立责任额。
func (m *naiveModel) stage(ps []naivePolicy, weights []int64, ilAmount, target int64) map[string]int64 {
	ils := map[string]int64{}
	for _, p := range ps {
		ils[p.no] = m.il(p, ilAmount)
	}
	pay := map[string]int64{}
	done := map[string]bool{}
	left := target

	for left > 0 {
		var total int64
		for i, p := range ps {
			if !done[p.no] && pay[p.no] < ils[p.no] {
				total += weights[i]
			}
		}
		if total == 0 {
			break
		}

		floors := map[string]int64{}
		var floorSum int64
		var over []string
		for i, p := range ps {
			if done[p.no] || pay[p.no] >= ils[p.no] || weights[i] == 0 {
				continue
			}
			r := new(big.Rat).Quo(
				new(big.Rat).Mul(big.NewRat(left, 1), big.NewRat(weights[i], 1)),
				big.NewRat(total, 1))
			fl := floorRat(r)
			floors[p.no] = fl
			floorSum += fl
			if fl > ils[p.no]-pay[p.no] {
				over = append(over, p.no)
			}
		}

		if len(over) > 0 {
			for i := 1; i < len(over); i++ {
				for j := i; j > 0 && over[j-1] > over[j]; j-- {
					over[j-1], over[j] = over[j], over[j-1]
				}
			}
			no := over[0]
			left -= ils[no] - pay[no]
			pay[no] = ils[no]
			done[no] = true
			continue
		}

		for no, fl := range floors {
			pay[no] += fl
		}
		left -= floorSum
		for left > 0 {
			best := ""
			for _, p := range ps {
				if done[p.no] || pay[p.no] >= ils[p.no] {
					continue
				}
				if best == "" || ils[p.no] > ils[best] ||
					(ils[p.no] == ils[best] && p.no < best) {
					best = p.no
				}
			}
			if best == "" {
				return pay
			}
			pay[best]++
			left--
		}
	}
	return pay
}

func (m *naiveModel) accept(lossNo string, day, amount int64) map[string]int64 {
	var ps []naivePolicy
	for _, p := range m.policies {
		if p.rem > 0 && day >= p.start && day < p.end {
			ps = append(ps, p)
		}
	}
	result := map[string]int64{}
	if len(ps) == 0 {
		m.record(lossNo, day, amount, nil)
		return result
	}

	var prim, exc []naivePolicy
	hasInd := false
	for _, p := range ps {
		if p.clause == Excess {
			exc = append(exc, p)
		} else {
			prim = append(prim, p)
			if p.clause == IndependentShare {
				hasInd = true
			}
		}
	}

	paid := int64(0)
	if len(prim) > 0 {
		w := make([]int64, len(prim))
		if hasInd {
			for i, p := range prim {
				w[i] = m.il(p, amount)
			}
		} else {
			for i, p := range prim {
				w[i] = p.per
				if w[i] > p.rem {
					w[i] = p.rem
				}
			}
		}
		for no, v := range m.stage(prim, w, amount, amount) {
			result[no] = v
			paid += v
		}
	}
	if paid < amount && len(exc) > 0 {
		w := make([]int64, len(exc))
		for i, p := range exc {
			w[i] = m.il(p, amount)
		}
		for no, v := range m.stage(exc, w, amount, amount-paid) {
			result[no] += v
		}
	}

	ded := map[string]int64{}
	for no, v := range result {
		if v <= 0 {
			continue
		}
		ded[no] = v
		for i := range m.policies {
			if m.policies[i].no == no {
				m.policies[i].rem -= v
			}
		}
	}
	m.record(lossNo, day, amount, ded)
	return result
}

func (m *naiveModel) record(lossNo string, day, amount int64, ded map[string]int64) {
	if m.deduct == nil {
		m.deduct = map[string]map[string]int64{}
		m.lossAmt = map[string]int64{}
		m.lossDay = map[string]int64{}
	}
	m.order = append(m.order, lossNo)
	m.deduct[lossNo] = ded
	m.lossAmt[lossNo] = amount
	m.lossDay[lossNo] = day
}

// undo 仅允许撤销末笔；失败（不存在/非末笔）不改状态。
func (m *naiveModel) undo(lossNo string) bool {
	if len(m.order) == 0 || m.order[len(m.order)-1] != lossNo {
		return false
	}
	for no, v := range m.deduct[lossNo] {
		for i := range m.policies {
			if m.policies[i].no == no {
				m.policies[i].rem += v
			}
		}
	}
	m.order = m.order[:len(m.order)-1]
	delete(m.deduct, lossNo)
	delete(m.lossAmt, lossNo)
	delete(m.lossDay, lossNo)
	return true
}

func (m *naiveModel) remaining() map[string]int64 {
	r := map[string]int64{}
	for _, p := range m.policies {
		r[p.no] = p.rem
	}
	return r
}
