// Package vc 定义向量时钟类型与投递判定的纯函数，不依赖其他包。
package vc

// Vector 是向量时钟：Vector[k] 表示本节点已知的「节点 k 已广播的第几条消息」。
type Vector []int

// New 返回长度为 n 的零向量。
func New(n int) Vector { return make(Vector, n) }

// Clone 返回 v 的副本。
func (v Vector) Clone() Vector {
	c := make(Vector, len(v))
	copy(c, v)
	return c
}

// Equal 逐字段比较两个向量。
func (v Vector) Equal(o Vector) bool {
	if len(v) != len(o) {
		return false
	}
	for k := range v {
		if v[k] != o[k] {
			return false
		}
	}
	return true
}

// Deliverable 判断携带时间戳 ts、发送者为 q 的消息能否投递到当前时钟为 p 的节点：
// ts[q] 必须恰好是 p 已知的 q 的下一条（不跳号），且 q 广播前已投递的其它节点
// 的消息（ts[k], k != q）p 也必须已全部投递。
func Deliverable(p, ts Vector, q int) bool {
	if q < 0 || q >= len(p) || len(ts) != len(p) {
		return false
	}
	if ts[q] != p[q]+1 {
		return false
	}
	for k := range p {
		if k != q && ts[k] > p[k] {
			return false
		}
	}
	return true
}
