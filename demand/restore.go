package demand

// chooseRestore 按优先级顺序逐个尝试恢复；
// 尝试某负荷后若预测越限，则该负荷及其后的负荷本次都不恢复（不得跳过）。
//
// cands 已按"优先级数字升序、同级编号升序"排好。
// feasible(addedKW) 判定累计恢复 addedKW 后是否所有窗口均不越限。
func chooseRestore(cands []*loadState, feasible func(addedKW int64) bool) []int {
	var restored []int
	var added int64
	for _, l := range cands {
		if !feasible(added + l.spec.RatedKW) {
			break
		}
		added += l.spec.RatedKW
		restored = append(restored, l.spec.ID)
	}
	return restored
}
