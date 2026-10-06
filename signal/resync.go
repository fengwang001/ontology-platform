package signal

// resync.go 相位差回归：偏离量归一化与单循环绿时调整分配。

// normDev 将偏离量归一化到 (-c/2, c/2]；恰为半周期时保持正值（取延长方向）。
func normDev(x, c int64) int64 {
	r := x % c
	if r < 0 {
		r += c
	}
	if 2*r > c {
		r -= c
	}
	return r
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// adjustCycle 在循环回绕时按偏离量调整本循环各相位工作绿时。
// 每个循环总调整量 <= MaxAdjust，且各相位仍在 [MinGreen, MaxGreen] 内。
// 方向：偏离 > 0（实际偏晚）缩短；偏离 < 0 或恰为半周期时延长。
func (e *engine) adjustCycle(p int, t int64) {
	for i := range e.cycGreen {
		e.cycGreen[i] = int64(e.plan.Greens[i])
	}
	d := normDev(t-e.plan.Offset-e.prefix[p], e.cycleLen)
	if d == 0 || e.plan.MaxAdjust <= 0 {
		e.lastAdjust = 0
		return
	}
	budget := e.plan.MaxAdjust
	if ad := abs64(d); ad < budget {
		budget = ad
	}
	extend := d < 0 || 2*d == e.cycleLen
	rem := budget
	for i := 0; i < e.n && rem > 0; i++ {
		var can int64
		if extend {
			can = int64(e.phases[i].MaxGreen) - e.cycGreen[i]
		} else {
			can = e.cycGreen[i] - int64(e.phases[i].MinGreen)
		}
		take := min(can, rem)
		if extend {
			e.cycGreen[i] += take
		} else {
			e.cycGreen[i] -= take
		}
		rem -= take
	}
	e.lastAdjust = budget - rem
}
