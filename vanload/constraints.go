package vanload

// feasibility 表示一件货物相对当前状态的放置判定结果。
type feasibility struct {
	chosen  int                // 可行时选定的分区编号（1 起）；不可行为 0
	reasons map[int]RejectKind // 不可行时各分区首个不满足的约束
	overall RejectKind         // 归并后的整体原因
}

// evaluateFeasibility 在给定状态上判定货物 c 应放入的分区。
// 不修改状态。只扫描分区聚合信息，不枚举在车货物。
func evaluateFeasibility(s *state, c Cargo) feasibility {
	n := len(s.states)

	// prefixMin[k] = 分区 1..k（含）中最小停靠点序号；suffixMax[k] = 分区 k..N 中最大值。
	// 评估目标 k 时用 prefixMin[k-1] 与 suffixMax[k+1]。
	// 二者只依赖分区聚合极值，与车上货物总件数无关。
	prefixMin := make([]int, n+1)
	for j := 1; j <= n; j++ {
		prefixMin[j] = minStop(prefixMin[j-1], s.states[j-1].minStop)
	}
	suffixMax := make([]int, n+2)
	for j := n; j >= 1; j-- {
		suffixMax[j] = maxInt(suffixMax[j+1], s.states[j-1].maxStop)
	}

	reasons := make(map[int]RejectKind)
	chosen := 0
	for k := 1; k <= n; k++ {
		kind := compartmentFailure(s, k, c, prefixMin[k-1], suffixMax[k+1])
		if kind == KindNone {
			if chosen == 0 {
				chosen = k
			}
			continue
		}
		reasons[k] = kind
	}

	f := feasibility{chosen: chosen}
	if chosen == 0 {
		f.reasons = reasons
		f.overall = mergeReasons(reasons)
	}
	return f
}

// compartmentFailure 返回某分区首个不满足的约束；KindNone 表示可行。
// 检查顺序即严重度由高到低：顺序冲突、隔离冲突、超重、超容。
// frontMin 为前方各分区在车货物的最小停靠点序号（0 表示前方无货）；
// behindMax 为后方各分区在车货物的最大停靠点序号（0 表示后方无货）。
func compartmentFailure(s *state, idx int, c Cargo, frontMin, behindMax int) RejectKind {
	cs := s.states[idx-1]

	// 顺序约束：卸货更早（序号更小）的货物不得位于更靠前（编号更小）的分区。
	// 前方分区存在比本货更早卸的货物，或后方分区存在比本货更晚卸的货物，即冲突。
	if frontMin > 0 && frontMin < c.Stop {
		return KindOrderConflict
	}
	if behindMax > c.Stop {
		return KindOrderConflict
	}

	// 隔离约束：易燃与氧化不同区；食品不与易燃、氧化任一类同区。
	switch c.Category {
	case CategoryFlammable:
		if cs.oxidizingCount > 0 || cs.foodCount > 0 {
			return KindIsolationConflict
		}
	case CategoryOxidizing:
		if cs.flammableCount > 0 || cs.foodCount > 0 {
			return KindIsolationConflict
		}
	case CategoryFood:
		if cs.flammableCount > 0 || cs.oxidizingCount > 0 {
			return KindIsolationConflict
		}
	}

	// 载重约束：恰好等于上限允许。
	limit := s.compartments[idx-1]
	if cs.usedWeight+c.Weight > limit.MaxWeight {
		return KindOverweight
	}

	// 容积约束：恰好等于上限允许。
	if cs.usedVolume+c.Volume > limit.MaxVolume {
		return KindOvervolume
	}
	return KindNone
}

// mergeReasons 在各分区原因中取严重度最低的一类。
func mergeReasons(reasons map[int]RejectKind) RejectKind {
	worst := KindNone
	worstRank := -1
	for _, kind := range reasons {
		if r := severityRank(kind); r > worstRank {
			worstRank = r
			worst = kind
		}
	}
	return worst
}

// minStop 在两个停靠点序号中取最小，0 视为“无货”忽略。
func minStop(a, b int) int {
	if a == 0 {
		return b
	}
	if b == 0 {
		return a
	}
	if b < a {
		return b
	}
	return a
}

func maxInt(a, b int) int {
	if b > a {
		return b
	}
	return a
}
