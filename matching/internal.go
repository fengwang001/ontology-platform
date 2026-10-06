package matching

// order 是委托在引擎内部的完整状态。
type order struct {
	clientID  int64
	seq       int64 // 接受序号；加量改量后更新为新序号（失去时间优先）
	side      Side
	price     int64
	typ       OrderType
	total     int64 // 当前总量
	filled    int64 // 已成交量
	remaining int64 // 剩余总量 = total - filled
	showParam int64 // 冰山显示量参数
	status    Status

	// 簿内定位（仅当委托处于挂簿/部分成交且在簿上时非 nil）。
	batch  *batch      // 冰山/普通：当前显示批
	hidden *hiddenNode // 隐藏：所在隐藏队列节点
	lev    *level
}

func (o *order) inBook() bool { return o.batch != nil || o.hidden != nil }
