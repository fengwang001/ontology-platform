package runtimefilter

import "cmp"

// orderedKey 是支持最小/最大值比较的连接键类型约束。
type orderedKey interface {
	cmp.Ordered
}
