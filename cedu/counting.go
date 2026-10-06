package cedu

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

// rawCredit 为某周期窗口内三类原始学分汇总。
// electiveRaw 已包含按选修计入的上周期结转学分。
type rawCredit struct {
	requiredRaw int
	electiveRaw int
	onlineRaw   int
}

// counted 为应用全部计入规则后的结果。
type counted struct {
	requiredIn int
	electiveIn int
	onlineIn   int
	totalIn    int
}

// countCredit 应用周期计入规则：
//  1. 必修、选修各自按计入上限封顶（线上类无独立上限）；
//  2. 线上类计入量不得超过必修与选修封顶后之和；
//  3. 多个约束同时成立时取使总计入量最大者——规则 1、2 顺序应用
//     即为逐类取上界，各取最大，结果是联合可行域内总量最大的点。
func countCredit(r rawCredit, cfg Config) counted {
	req := minInt(r.requiredRaw, cfg.RequiredCap)
	ele := minInt(r.electiveRaw, cfg.ElectiveCap)
	onl := minInt(r.onlineRaw, req+ele)
	return counted{
		requiredIn: req,
		electiveIn: ele,
		onlineIn:   onl,
		totalIn:    req + ele + onl,
	}
}

// met 判断周期达标：总量达标且必修达标，两者同时满足。
func (c counted) met(cfg Config) bool {
	return c.totalIn >= cfg.TotalRequired && c.requiredIn >= cfg.RequiredMin
}

// carryOut 计算周期达标后的结转量：
//   - 仅超额总量可结转，且不超过结转上限；
//   - 必修超额部分不构成结转来源，结转只能来自选修与线上超额；
//   - 结转来源 = 封顶后(选修+线上) 减去 满足总量要求后可归于它们的额度。
//
// 宽限期补足（graceMet=true）的周期不产生任何结转，由调用方传 0。
func carryOut(r rawCredit, c counted, cfg Config) int {
	surplus := maxInt(0, c.totalIn-cfg.TotalRequired)
	source := maxInt(0, c.electiveIn+c.onlineIn-maxInt(0, cfg.TotalRequired-c.requiredIn))
	// source <= surplus 恒成立（source 是按“先扣必修”口径的超额，
	// 必修超额不结转，因此 source 可能严格小于 surplus）。
	return minInt(cfg.CarryoverCap, minInt(surplus, source))
}

// cycleBounds 返回周期 k（0 起）的左闭右开区间 [start,end)。
func cycleBounds(issueDate, cycleLength, k int) (start, end int) {
	start = issueDate + k*cycleLength
	end = start + cycleLength
	return
}
