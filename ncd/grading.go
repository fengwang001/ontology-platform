package ncd

// gradeNext 由上一保单年度的出险汇总裁定续保等级，纯函数：
//   - 有责出险达到 3 次及以上：直接降为 0（保护不影响该次数统计）；
//   - 无有责出险：升一级，最高级不再升；
//   - 有责出险每次降两级，降级不低于 0；
//   - 保护只抵消该年度第一次有责出险的降级（不因此转为升级）。
func gradeNext(level, liable int, protected bool, maxLevel int) int {
	if liable >= 3 {
		return 0
	}
	effective := liable
	if protected && effective > 0 {
		effective--
	}
	if liable == 0 {
		return min(level+1, maxLevel)
	}
	if effective == 0 {
		return level
	}
	return max(level-2*effective, 0)
}
