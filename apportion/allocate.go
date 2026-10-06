package apportion

import (
	"cmp"
	"math/big"
	"slices"
)

// participant 分摊计算中一张保单的视图。
type participant struct {
	no     string // 保单编号
	weight int64  // 分摊权重
	cap    int64  // 独立责任额（应赔上限）
}

// floorMulDiv 精确计算 floor(a*b/d)，d>0，a、b 非负。
// 用 big.Int 避免 int64 乘法溢出，保证结果可精确复现。
func floorMulDiv(a, b, d int64) int64 {
	r := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	r.Quo(r, big.NewInt(d))
	return r.Int64()
}

// shareReachesCap 报告精确比例份额 budget*w/W 是否达到剩余空间 room。
func shareReachesCap(budget, w, room, totalW int64) bool {
	l := new(big.Int).Mul(big.NewInt(budget), big.NewInt(w))
	r := new(big.Int).Mul(big.NewInt(room), big.NewInt(totalW))
	return l.Cmp(r) >= 0
}

// distribute 在 participants 之间按权重比例分摊 budget，
// 每张不超过其 cap；尾差逐分分配给独立责任额最大且未达 cap 者，
// 并列时给保单编号字典序最小者。返回与 parts 同序的分配额。
func distribute(parts []participant, budget int64) []int64 {
	alloc := make([]int64, len(parts))
	if budget <= 0 {
		return alloc
	}
	var totalCap int64
	for _, p := range parts {
		totalCap += p.cap
	}
	if budget > totalCap {
		budget = totalCap
	}
	active := make([]int, 0, len(parts))
	for i := range parts {
		active = append(active, i)
	}
	for budget > 0 && len(active) > 0 {
		var totalW int64
		for _, i := range active {
			totalW += parts[i].weight
		}
		if totalW == 0 {
			break
		}
		// 精确份额达到剩余空间者先按 cap 封顶，余额在其余保单间继续分配。
		capped := false
		rest := make([]int, 0, len(active))
		for _, i := range active {
			room := parts[i].cap - alloc[i]
			if shareReachesCap(budget, parts[i].weight, room, totalW) {
				alloc[i] += room
				budget -= room
				capped = true
			} else {
				rest = append(rest, i)
			}
		}
		if capped {
			active = rest
			continue
		}
		// 无人触顶：向下取整分配，再把尾差逐分按规则分配。
		var sum int64
		for _, i := range active {
			share := floorMulDiv(budget, parts[i].weight, totalW)
			alloc[i] += share
			sum += share
		}
		rem := budget - sum
		order := slices.Clone(active)
		slices.SortFunc(order, func(a, b int) int {
			if parts[a].cap != parts[b].cap {
				return cmp.Compare(parts[b].cap, parts[a].cap) // 独立责任额大者优先
			}
			return cmp.Compare(parts[a].no, parts[b].no) // 并列时编号字典序小者优先
		})
		for rem > 0 {
			progress := false
			for _, i := range order {
				if rem == 0 {
					break
				}
				if alloc[i] < parts[i].cap {
					alloc[i]++
					rem--
					progress = true
				}
			}
			if !progress {
				break
			}
		}
		budget = rem
	}
	return alloc
}

// apportion 对一次损失在参与保单（已按保单编号字典序）之间执行两阶段分摊。
func apportion(policies []*policyState, amount int64) []Payout {
	alloc := make([]int64, len(policies))
	// 第一阶段：非超额型保单。
	var stage1 []int
	hasIndependent := false
	for i, p := range policies {
		if p.policy.Clause == ClauseExcess {
			continue
		}
		stage1 = append(stage1, i)
		if p.policy.Clause == ClauseIndependentLiability {
			hasIndependent = true
		}
	}
	var paid1 int64
	if len(stage1) > 0 {
		parts := make([]participant, len(stage1))
		for j, i := range stage1 {
			weight := policies[i].independentLiability(amount)
			if !hasIndependent {
				weight = policies[i].weight()
			}
			parts[j] = participant{
				no:     policies[i].policy.PolicyNo,
				weight: weight,
				cap:    policies[i].independentLiability(amount),
			}
		}
		got := distribute(parts, amount)
		for j, i := range stage1 {
			alloc[i] = got[j]
			paid1 += got[j]
		}
	}
	// 第二阶段：仅当第一阶段未赔足时，超额型保单参与剩余部分。
	if paid1 < amount {
		var stage2 []int
		for i, p := range policies {
			if p.policy.Clause == ClauseExcess {
				stage2 = append(stage2, i)
			}
		}
		if len(stage2) > 0 {
			parts := make([]participant, len(stage2))
			for j, i := range stage2 {
				liab := policies[i].independentLiability(amount)
				parts[j] = participant{no: policies[i].policy.PolicyNo, weight: liab, cap: liab}
			}
			got := distribute(parts, amount-paid1)
			for j, i := range stage2 {
				alloc[i] = got[j]
			}
		}
	}
	var payouts []Payout
	for i, p := range policies {
		if alloc[i] > 0 {
			payouts = append(payouts, Payout{PolicyNo: p.policy.PolicyNo, Amount: alloc[i]})
		}
	}
	return payouts
}
