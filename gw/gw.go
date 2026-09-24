// Package gw 实现全局（永不关闭、永不重置）累积器与周期触发判定。
// 不依赖其他包。
package gw

// Acc 是全局累积器：只增不减，触发后保持原值继续累加。
type Acc struct {
	sum int64
	cnt int64
}

// Add 把一个元素的值并入累积器：先 sum+=v、cnt++。
// 这是全系统唯一修改 sum/cnt 的地方（不变量 2）。
func (a *Acc) Add(v int64) {
	a.sum += v
	a.cnt++
}

// Sum 返回已接受元素的累计和。
func (a *Acc) Sum() int64 { return a.sum }

// Cnt 返回已接受元素的累计个数。
func (a *Acc) Cnt() int64 { return a.cnt }

// Fired 在 Add 之后判定触发条件：cnt > 0 且 cnt 是 period 的正整数倍。
// 纯判定，绝不修改累积器（不变量 2）。
func (a *Acc) Fired(period int64) bool {
	return a.cnt > 0 && a.cnt%period == 0
}
