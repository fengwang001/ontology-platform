// Package agg 提供计数聚合与朴素参照实现，不依赖其他包。
package agg

// Add 把一段字符串逐项计入计数映射 m（原地修改）。
func Add(m map[string]int, seg []string) {
	for _, s := range seg {
		m[s]++
	}
}

// Naive 从头到尾重放整个 src，返回朴素计数结果。
// 它是所有重建结果必须一致的参照。
func Naive(src []string) map[string]int {
	want := make(map[string]int)
	Add(want, src)
	return want
}
