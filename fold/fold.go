// Package fold 定义净零折叠的唯一判定规则：
// 一个操作是否与未了结序列末尾那条抵消（量值相等、符号相反）。
package fold

// Cancels 报告操作 op 是否与序列末尾条目 tail 抵消。
// 二者量值相等、符号相反时和为 0，即抵消。
// 调用方保证 op 与 tail 均为非零且非 math.MinInt64（量值可表示）。
func Cancels(tail, op int64) bool {
	return tail+op == 0
}
