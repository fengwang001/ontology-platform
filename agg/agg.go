// Package agg 提供计数聚合与朴素参照：把一段 []string 计入 map[string]int。
// 不依赖其他包。
package agg

// Apply 把 items 逐项计入 m（就地累加）。
func Apply(m map[string]int, items []string) {
	for _, s := range items {
		m[s]++
	}
}

// Replay 朴素参照：把整条 src 从头到尾重放计数，返回新映射。
func Replay(src []string) map[string]int {
	m := make(map[string]int, len(src))
	Apply(m, src)
	return m
}

// Equal 报告两个计数映射是否逐键相同。
func Equal(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// Clone 返回 m 的副本。
func Clone(m map[string]int) map[string]int {
	c := make(map[string]int, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}
