// Package fold 实现增量求和变更日志的折叠规则：
// 一个操作仅在与未了结序列末尾那条量值相等、符号相反时与之抵消。
// 本包不依赖工程内其他包。
package fold

// Cancels 判断带符号增量 op 是否抵消序列末尾 tail。
// 两条均非零且和为 0（量值相等、符号相反）时返回 true。
// changelog 中的元素恒非零（k 必须为正整数），零仅作防御处理。
func Cancels(tail, op int64) bool {
	return tail != 0 && op != 0 && tail+op == 0
}
