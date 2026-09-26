// Package vc 提供向量时钟类型与纯函数投递判定，不依赖其他包。
package vc

// Clock 是一个节点的向量时钟：VC[k] = 已知节点 k 已广播/投递到第几条。
type Clock []int

// Message 是一条广播消息：From 为广播者，TS 为广播时刻 From 的时钟副本。
type Message struct {
	From int
	TS   Clock
}

// Deliverable 判定接收者时钟 p 此刻能否投递来自 q、携带时间戳 ts 的消息：
// ts[q] 必须恰好是 p[q] 的下一条，且 q 已见的其它节点消息 p 也都已见。
func Deliverable(p Clock, ts Clock, q int) bool {
	if q < 0 || q >= len(ts) || q >= len(p) || len(p) != len(ts) {
		return false
	}
	if ts[q] != p[q]+1 { // 同源不跳号：必须恰好是下一条
		return false
	}
	for k := range ts {
		if k != q && ts[k] > p[k] { // 因果序：q 已见的跨节点消息 p 也必须已见
			return false
		}
	}
	return true
}
