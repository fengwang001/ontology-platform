// Package query 封装一次最长公共子串查询：长度、起始下标与具体子串内容。
// 依赖 dp。
package query

import "ontology/dp"

// Result 是一次查询的产出。
type Result struct {
	Length int    // 最长公共子串长度
	Start  int    // 子串在参照串 a 中的起始下标（0-based）
	Text   string // 子串内容，a[Start:Start+Length]
}

// Exec 在参照串 a 上执行一次对 b 的查询。core 必须以同一个 a 构建。
func Exec(a string, core *dp.Core, b string) Result {
	l, s := core.Longest(b)
	return Result{Length: l, Start: s, Text: a[s : s+l]}
}
