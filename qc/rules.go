package qc

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func sign(v int64) int {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	default:
		return 0
	}
}

// update 用本次偏离量推进单个水平的游程计数。
// 连续性的“同侧”要求相邻点同号；偏离量为零不属于任何一侧，清零全部游程。
func (ls *levelSeries) update(dev, sd int64) {
	over1 := abs(dev) > sd
	over2 := abs(dev) > 2*sd
	if ls.hasRun && dev != 0 && sign(dev) == sign(ls.lastDev) {
		ls.runSide++
		if over1 {
			ls.run1++
		} else {
			ls.run1 = 0
		}
		if over2 {
			ls.run2++
		} else {
			ls.run2 = 0
		}
	} else {
		if dev != 0 {
			ls.runSide = 1
		} else {
			ls.runSide = 0
		}
		if over1 {
			ls.run1 = 1
		} else {
			ls.run1 = 0
		}
		if over2 {
			ls.run2 = 1
		} else {
			ls.run2 = 0
		}
	}
	ls.lastDev = dev
	ls.hasRun = true
}

// evaluate 判定一次运行并推进两个水平的序列状态。
// 返回全部被触发的规则（按 RuleID 升序）与运行结论。
// 运行开销为 O(1)，与历史运行总数无关。
func (p *project) evaluate(lowValue, highValue int64) RunResult {
	lowDev := lowValue - p.low.target
	highDev := highValue - p.high.target

	p.lowSeries.update(lowDev, p.low.sd)
	p.highSeries.update(highDev, p.high.sd)

	lowOver2 := abs(lowDev) > 2*p.low.sd
	highOver2 := abs(highDev) > 2*p.high.sd

	var rules []RuleID
	// 规则一：任一水平单点超过 3 倍标准差。
	if abs(lowDev) > 3*p.low.sd || abs(highDev) > 3*p.high.sd {
		rules = append(rules, Rule1)
	}
	// 规则二：同一水平连续两点（含本次）同侧且都超过 2 倍标准差。
	if p.lowSeries.run2 >= 2 || p.highSeries.run2 >= 2 {
		rules = append(rules, Rule2)
	}
	// 规则三：本次运行两水平异侧，且各自超过 2 倍自身标准差。
	if lowOver2 && highOver2 && sign(lowDev) != sign(highDev) {
		rules = append(rules, Rule3)
	}
	// 规则四：同一水平连续四点（含本次）同侧且都超过 1 倍标准差。
	if p.lowSeries.run1 >= 4 || p.highSeries.run1 >= 4 {
		rules = append(rules, Rule4)
	}
	// 规则五：同一水平连续十点（含本次）都在同一侧。
	if p.lowSeries.runSide >= 10 || p.highSeries.runSide >= 10 {
		rules = append(rules, Rule5)
	}

	status := RunNormal
	if len(rules) > 0 {
		status = RunRejected
	} else if lowOver2 || highOver2 {
		status = RunWarning
	}
	return RunResult{Rules: rules, Status: status}
}
