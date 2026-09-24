// Package lc 实现单节点 Lamport 时钟规则与 (时间戳, 节点ID) 的比较。
// 本包不依赖项目内任何其他包。
package lc

// Clock 是单个节点的 Lamport 逻辑时钟，L 初始为 0。
type Clock struct {
	node int
	l    int64
}

// New 创建属于节点 node 的时钟。
func New(node int) *Clock { return &Clock{node: node} }

// Node 返回该时钟所属节点 ID。
func (c *Clock) Node() int { return c.node }

// Get 返回当前时钟值 L。
func (c *Clock) Get() int64 { return c.l }

// Local 执行本地事件推进：L = L + 1，返回新 L（即事件时间戳）。
func (c *Clock) Local() int64 {
	c.l++
	return c.l
}

// Send 执行发送事件推进：L = L + 1，返回消息携带的 t_msg。
// to == from 的自发消息在规则上与普通发送完全相同，由调用方处理。
func (c *Clock) Send() int64 {
	c.l++
	return c.l
}

// Recv 执行接收事件推进：L = max(L, tMsg) + 1，返回新 L。
// 取 max 是时钟条件（发送先于接收 ⇒ 时间戳严格增大）成立的关键。
func (c *Clock) Recv(tMsg int64) int64 {
	if tMsg > c.l {
		c.l = tMsg
	}
	c.l++
	return c.l
}

// Key 是全序键 (时间戳, 节点ID)。
type Key struct {
	TS   int64
	Node int
}

// MakeKey 构造全序键。
func MakeKey(ts int64, node int) Key { return Key{TS: ts, Node: node} }

// Less 报告全序 a < b：先比时间戳，时间戳相同则节点 ID 小者在前。
// 与事件的执行先后无关。
func Less(a, b Key) bool {
	if a.TS != b.TS {
		return a.TS < b.TS
	}
	return a.Node < b.Node
}
