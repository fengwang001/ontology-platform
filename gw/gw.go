// Package gw 持有单个 Key 的全局累积器与全局窗口的触发判定。
// 全局窗口永不关闭、永不重置：Add 只做累加，触发判定纯函数、不修改任何状态。
package gw

// Acc 是一个 Key 的全局累积器，初始 Sum/Cnt 均为零值。
type Acc struct {
	Sum int64
	Cnt int64
}

// Add 先把 val 并入累计和、再把计数加一。
// 触发判定只能在 Add 之后做，因此触发快照必然包含刚加入的元素。
func (a *Acc) Add(val int64) {
	a.Sum += val
	a.Cnt++
}

// Triggered 报告“加入当前元素之后” cnt 是否恰好是 period 的正整数倍。
// 它只读取状态：即使返回 true，累积器也保持原样、继续累加。
func Triggered(cnt, period int64) bool {
	return cnt > 0 && cnt%period == 0
}
