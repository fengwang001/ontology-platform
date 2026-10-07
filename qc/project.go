package qc

// levelParam 一个质控水平的靶值与标准差。
type levelParam struct {
	target int64
	sd     int64
}

// levelSeries 单个水平的连续运行状态，用于 O(1) 判定。
// 只保存判定规则二/四/五所需的游程计数，不保存历史序列。
type levelSeries struct {
	lastDev int64 // 最近一次运行的偏离量；hasRun 为 false 时无意义
	run2    int   // 连续同号且 |dev| > 2*sd 的点数（遇不满足清零）
	run1    int   // 连续同号且 |dev| > 1*sd 的点数
	runSide int   // 连续同号（同侧）点数，偏离量为零时清零
	hasRun  bool  // 校准后或登记后尚无运行
}

// reportNode 报告链表节点。链表只保存“可被未来失控追溯标记”的报告：
// 即出具时刻晚于当前最近一次非失控运行时刻的报告
// （若从无非失控运行，则为全部已出具报告）。
// 不变式由两个方向维护：
//   - 出具时，若已存在非失控运行且出具时刻不晚于其时刻，
//     该报告永不可被标记（未来区间起点只增不减），直接不入链；
//   - 每次非失控运行把链表清空（链上报告的时刻必然不晚于新的
//     最近一次非失控运行时刻，全部失去被标记资格）。
//
// 因此失控追溯时链上节点恰好就是待标记报告，扫描开销只与
// 待标记报告数相关，与历史报告总数无关。
type reportNode struct {
	id   int64
	time int64
	next *reportNode
}

// project 一个（仪器，项目）登记项的全部状态。
type project struct {
	low, high levelParam
	validity  int64

	lowSeries, highSeries levelSeries

	state          ProjectState
	cleanStreak    int   // 失控后连续“未触发规则且非警告”的运行次数
	lastNonRejTime int64 // 最近一次非失控运行时刻
	hasRun         bool  // 是否有过任何运行（校准不改变）
	hasNonRej      bool  // 是否有过非失控运行（校准不改变）

	head, tail *reportNode // 可被追溯标记的报告链表（出具顺序）
}
