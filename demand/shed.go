package demand

import (
	"math/big"
	"sort"
)

// shedPlan 为一次切除决策的结果。
type shedPlan struct {
	ids         []int // 被切除负荷编号（升序）
	stillExceed bool  // 全部可切除负荷切除后是否仍越限
	required    *frac // 所需最小切除容量（千瓦，精确值）
}

// chooseShed 在可切除负荷中按三层并列规则选出最优切除集合：
//  1. 使所有窗口预测不越限（feasible(power)）；
//  2. 负荷个数最少；
//  3. 并列取优先级数字之和最大；
//  4. 仍并列取编号序列字典序最小。
//
// 实现：
//   - 所需容量是精确分数，额定容量为整数，先取 minKW=ceil(required)；
//   - 用 0/1 背包 DP（容量维度）求达到 minKW 的最少负荷数 minCount，
//     复杂度 O(负荷数 × 总容量)，与历史长度无关；
//   - 仅在 minCount 层枚举组合（容量上界剪枝），按第 3、4 层规则选最优。
func chooseShed(cands []*loadState, required *frac, feasible func(shedKW int64) bool) shedPlan {
	plan := shedPlan{required: required.clone()}
	if required.cmp(zero) <= 0 {
		return plan
	}

	var total int64
	for _, l := range cands {
		total += l.spec.RatedKW
	}
	if !feasible(total) {
		plan.ids = make([]int, 0, len(cands))
		for _, l := range cands {
			plan.ids = append(plan.ids, l.spec.ID)
		}
		sort.Ints(plan.ids)
		plan.stillExceed = true
		return plan
	}

	minKW := ceilFrac(required)
	if minKW <= 0 {
		minKW = 1
	}

	const inf = 1 << 30
	dp := make([]int, total+1)
	for p := int64(1); p <= total; p++ {
		dp[p] = inf
	}
	for _, l := range cands {
		w := l.spec.RatedKW
		for p := total; p >= w; p-- {
			if dp[p-w]+1 < dp[p] {
				dp[p] = dp[p-w] + 1
			}
		}
	}
	minCount := inf
	for p := minKW; p <= total; p++ {
		if dp[p] < minCount {
			minCount = dp[p]
		}
	}
	if minCount == inf {
		return shedAll(plan, cands, true)
	}

	// 枚举基数恰为 minCount 的组合。候选按编号升序，便于字典序比较。
	ordered := append([]*loadState(nil), cands...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].spec.ID < ordered[j].spec.ID })
	m := len(ordered)

	// reach[need]：从对应后缀里选 need 个负荷能得到的容量集合（位集）。
	// 枚举时只对"最终容量可能 >= minKW"的前缀展开，精确剪枝。
	// 后缀集合随 DFS 起点变化，这里用按 (start,need) 记忆化的方式构建。
	reachCache := map[[2]int]*big.Int{}
	var reach func(start, need int) *big.Int
	reach = func(start, need int) *big.Int {
		if need == 0 {
			return big.NewInt(1) // 容量 0
		}
		if start >= m || need > m-start {
			return big.NewInt(0)
		}
		key := [2]int{start, need}
		if v, ok := reachCache[key]; ok {
			return v
		}
		without := reach(start+1, need)
		withTail := reach(start+1, need-1)
		w := ordered[start].spec.RatedKW
		withShift := new(big.Int).Lsh(withTail, uint(w))
		res := new(big.Int).Or(without, withShift)
		reachCache[key] = res
		return res
	}

	var bestIdx []int
	bestPri := int64(-1)
	found := false

	var dfs func(start int, chosen []int, power, priSum int64)
	dfs = func(start int, chosen []int, power, priSum int64) {
		if len(chosen) == minCount {
			if power >= minKW && feasible(power) {
				if !found || priSum > bestPri ||
					(priSum == bestPri && idxLexLess(chosen, bestIdx, ordered)) {
					found = true
					bestPri = priSum
					bestIdx = append(bestIdx[:0], chosen...)
				}
			}
			return
		}
		need := minCount - len(chosen)
		for i := start; i <= m-need; i++ {
			l := ordered[i]
			nextPower := power + l.spec.RatedKW
			// 选 i 后还需 need-1 个：可达容量集合为 nextPower + reach(i+1,need-1)。
			rest := reach(i+1, need-1)
			if !canReachFeasible(rest, nextPower, minKW, total) {
				continue
			}
			dfs(i+1, append(chosen, i), nextPower,
				priSum+int64(l.spec.Priority))
		}
	}
	dfs(0, nil, 0, 0)

	if !found {
		return shedAll(plan, ordered, true)
	}

	plan.ids = make([]int, 0, len(bestIdx))
	for _, idx := range bestIdx {
		plan.ids = append(plan.ids, ordered[idx].spec.ID)
	}
	sort.Ints(plan.ids)
	return plan
}

func shedAll(plan shedPlan, loads []*loadState, still bool) shedPlan {
	plan.ids = make([]int, 0, len(loads))
	for _, l := range loads {
		plan.ids = append(plan.ids, l.spec.ID)
	}
	sort.Ints(plan.ids)
	plan.stillExceed = still
	return plan
}

// idxLexLess 比较两个等长下标序列对应负荷编号的字典序。
func idxLexLess(a, b []int, ordered []*loadState) bool {
	for i := range a {
		ai, bi := ordered[a[i]].spec.ID, ordered[b[i]].spec.ID
		if ai != bi {
			return ai < bi
		}
	}
	return false
}

// canReachFeasible 判断容量集合 base+rest（rest 为位集）中是否存在
// >= needKW 的容量（更大容量只会更可行，feasible 单调，故检查集合最大值即可；
// 同时要求集合本身非空）。
func canReachFeasible(rest *big.Int, base, needKW, total int64) bool {
	if rest.Sign() == 0 {
		return false
	}
	maxOff := int64(rest.BitLen() - 1)
	return base+maxOff >= needKW
}
