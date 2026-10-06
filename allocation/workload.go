package allocation

import "math/bits"

// scalePercent 根据班级规模返回命中的规模系数（整数百分数）。
// 档位边界取等归高档：规模 x 命中满足 MinSize <= x 的最高档。
func scalePercent(tiers []ScaleTier, classSize int) (int, *Error) {
	chosen := 0
	chosenIdx := -1
	for i := range tiers {
		t := tiers[i]
		if t.MinSize <= classSize && t.MinSize >= chosen {
			chosen = t.MinSize
			chosenIdx = i
		}
	}
	if chosenIdx < 0 {
		return 0, newErr(ErrInvalidArgument, "class size %d below smallest tier", classSize)
	}
	return tiers[chosenIdx].Percent, nil
}

const percentBase = 100

// computeWorkload 一次性连乘后向下取整：
// floor(hours * scalePct * newPct * labPct / 100^3)。
// 非新开课时 newPct=100。不得分步取整。
func computeWorkload(hours, scalePct, newBonusPct, labPct int, isNew bool) (int, *Error) {
	newPct := percentBase
	if isNew {
		newPct = percentBase + newBonusPct
	}
	if hours <= 0 || scalePct <= 0 || newPct <= 0 || labPct <= 0 {
		return 0, newErr(ErrInvalidArgument, "non-positive factor in workload computation")
	}
	// 三个百分数系数连乘后与学时相乘，最后一次性除以 100^2 向下取整。
	// 全程 128 位运算杜绝中间步骤溢出；最终分子超出 int64 范围按参数非法处理。
	hi, lo := bits.Mul64(uint64(scalePct), uint64(newPct))
	hi, lo = mul128x64(hi, lo, uint64(labPct))
	hi, lo = mul128x64(hi, lo, uint64(hours))
	const int64MaxUint = uint64(1<<63 - 1)
	if hi != 0 || lo > int64MaxUint {
		return 0, newErr(ErrInvalidArgument, "workload overflow")
	}
	return int(lo / (percentBase * percentBase * percentBase)), nil
}

// taskWorkload 按任务自身的新开/实验属性选取系数折算。
func taskWorkload(cfg Config, hours, scalePct int, isNew, isLab bool) (int, *Error) {
	labPct := percentBase
	if isLab {
		labPct = cfg.LabPercent
	}
	return computeWorkload(hours, scalePct, cfg.NewCourseBonusPercent, labPct, isNew)
}

// mul128x64 计算 (hi:lo)*c，返回 128 位结果。
func mul128x64(hi, lo, c uint64) (uint64, uint64) {
	ph, pl := bits.Mul64(lo, c) // lo * c
	qh, ql := bits.Mul64(hi, c) // hi * c，ql 叠加到结果高字，qh 为超出 128 位部分
	resHi, carry := bits.Add64(ph, ql, 0)
	resHi += qh + carry // 超出 128 位的部分直接累加（本域输入极小不会发生，保持定义完整）
	return resHi, pl
}
