package cpe

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// countCredits 应用各类封顶与线上联合约束：
// 必修、选修各自封顶；线上计入量不超过必修与选修封顶后计入量之和；
// 多约束并存时取使总计入量最大者（此处贪心即最优）。
func countCredits(t Tally, cfg Config) Counted {
	m := minInt(t.Mandatory, cfg.MandatoryCap)
	e := minInt(t.Elective, cfg.ElectiveCap)
	o := minInt(t.Online, m+e)
	return Counted{Mandatory: m, Elective: e, Online: o, Total: m + e + o}
}

// evaluate 判定达标：总计入量不低于总要求，且必修计入量不低于必修最低要求。
func evaluate(c Counted, cfg Config) (meetsTotal, meetsMandatory, pass bool) {
	meetsTotal = c.Total >= cfg.TotalRequired
	meetsMandatory = c.Mandatory >= cfg.MandatoryMin
	return meetsTotal, meetsMandatory, meetsTotal && meetsMandatory
}

// carryover 计算结转到下一周期的量：
// 仅由选修类与线上类的超额部分构成（必修超额不结转），且不超过结转上限。
func carryover(c Counted, cfg Config) int {
	excess := c.Total - cfg.TotalRequired
	if excess <= 0 {
		return 0
	}
	nonMandatory := c.Elective + c.Online
	reqLeftForNonMandatory := maxInt(0, cfg.TotalRequired-c.Mandatory)
	nonMandatoryExcess := maxInt(0, nonMandatory-reqLeftForNonMandatory)
	return minInt(minInt(excess, nonMandatoryExcess), cfg.CarryoverCap)
}
