package ontology

// Pair 是一条满足连接条件的左右事件配对。区间两端均闭合。
type Pair struct {
	Key       string // 连接键
	Left      Event  // 左侧事件
	Right     Event  // 右侧事件
	LeftTime  int64  // 左事件时间
	RightTime int64  // 右事件时间
}
