package surge

// targetTier 依据比率求目标档：阈值满足 ratio>=threshold 的最高档；
// 无阈值达到为基础档 0。可用运力为零且待派为正视为无穷大。
func targetTier(thresholds []float64, pending, available int) (int, bool) {
	if available == 0 {
		if pending > 0 {
			return len(thresholds), true
		}
		return 0, false
	}
	ratio := float64(pending) / float64(available)
	target := 0
	for i, t := range thresholds {
		if ratio >= t {
			target = i + 1
		}
	}
	return target, false
}

// applyEvaluation 是档位状态机纯函数：依据目标档迁移当前档、
// 维护下调连续确认计数，并返回是否发生档位变化（每次最多降一档）。
func applyEvaluation(curTier, confirms, target, needConfirms int) (newTier, newConfirms int, changed bool) {
	switch {
	case target > curTier:
		return target, 0, true
	case target < curTier:
		if confirms+1 >= needConfirms {
			return curTier - 1, 0, true
		}
		return curTier, confirms + 1, false
	default:
		return curTier, 0, false
	}
}
