// Package meterengine 实现电表换表与读数衔接引擎。
//
// 一个供电点在其生命周期内先后挂接多只电表；引擎处理换表瞬间衔接、
// 显示值翻转判定、估算占位与实抄替代、乱序插入的翻转守恒，
// 并对任意两个已有读数时刻给出跨表用电量与估算参与标记。
//
// 错误严格按固定优先级返回：
//
//	参数非法 > 不在挂接期内 > 与已有读数冲突 > 读数冲突 > 不合理 > 无读数
//
// 所有方法可并发调用，语义等价于某个串行执行顺序；
// 被拒绝的操作不改变任何读数、挂接与换表记录。
package meterengine

// ReadingType 区分实抄与估算读数。
type ReadingType int

const (
	Actual ReadingType = iota + 1
	Estimated
)

// Meter 为一只电表的静态属性。
type Meter struct {
	id         string
	digits     int
	multiplier int64
	maxDisplay int64
	modulus    int64
}

// Reading 为某电表在某时刻的一条登记。
type Reading struct {
	time     int64
	display  int64
	kind     ReadingType
	fromSwap bool
}

// Mount 为一次挂接记录。
type Mount struct {
	point   string
	meter   *Meter
	install int64
	remove  int64
	initial int64
}

// QueryResult 为一次跨表用电量查询结果。
type QueryResult struct {
	Energy       int64
	HasEstimated bool
}
