package ncd

// Catchup 保费差额追补记录：追溯重定级使某次续保等级降低时产生。
type Catchup struct {
	YearIndex  int   // 被重定级的保单年度下标
	OldGrade   int   // 原等级
	NewGrade   int   // 新等级
	OldPremium int64 // 原等级保费
	NewPremium int64 // 新等级保费
	Diff       int64 // 差额 = 新等级保费 - 原等级保费
}

// adjudicate 单年度定级：由上一年度等级与出险记录裁定下一年度等级。
//
// 规则：无有责出险升一级（最高级不再升）；每次有责出险降两级；
// 有责达到三次及以上直接降为 0；降级不低于 0；无责出险不影响。
// 年度已购保护时，第一次有责出险不计入降级，但不影响三次归零的次数统计。
//
// 只读取上一年度，开销与该被保人历史年度数无关。
func (e *Engine) adjudicate(prevGrade int, prev *Year) int {
	e.adjudications++
	liable := 0
	for _, c := range prev.Claims {
		if c.Ratio >= e.cfg.LiabilityThreshold {
			liable++
		}
	}
	if liable >= 3 {
		return 0
	}
	if liable == 0 {
		if prevGrade+1 > e.cfg.MaxGrade {
			return e.cfg.MaxGrade
		}
		return prevGrade + 1
	}
	counted := liable
	if prev.Protected {
		counted-- // 保护抵消第一次有责出险的降级
	}
	g := prevGrade - 2*counted
	if g < 0 {
		g = 0
	}
	return g
}

// regrade 追溯重定级：从事故日所在年度的下一年度 from 起，
// 沿连续续保链逐年重新裁定等级，直到等级稳定为止。
//
// 重定级范围严格限于事故年度之后的年度；中断（非续保）年度等级恒为 0，
// 其后续年度不受影响，可提前终止。等级降低时产生保费差额追补记录；
// 等级升高不产生退费。
func (e *Engine) regrade(ins *Insured, from int) {
	for i := from; i < len(ins.Years); i++ {
		y := ins.Years[i]
		if !y.ByRenewal {
			return // 中断年度等级恒为 0，不随历史变化，链到此为止
		}
		ng := e.adjudicate(ins.Years[i-1].Grade, ins.Years[i-1])
		if ng == y.Grade {
			return // 等级稳定，后续年度的裁定输入不变
		}
		if ng < y.Grade {
			old := y.Grade
			ins.Catchups = append(ins.Catchups, Catchup{
				YearIndex:  i,
				OldGrade:   old,
				NewGrade:   ng,
				OldPremium: e.cfg.Premiums[old],
				NewPremium: e.cfg.Premiums[ng],
				Diff:       e.cfg.Premiums[ng] - e.cfg.Premiums[old],
			})
		}
		y.Grade = ng
	}
}
