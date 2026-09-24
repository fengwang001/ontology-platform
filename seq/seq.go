// Package seq 序号分配：持久 nextSeq 与空洞区间判定。
// 本包不依赖工程内其他包。
package seq

// Counter 是持久序号状态：next 单调不减，崩溃后保留。
// 零值不可用，必须用 NewCounter 构造（序号从 1 起）。
type Counter struct {
	next int // 下一个要分配的序号，从 1 起，只增不减
}

// NewCounter 返回 nextSeq=1 的计数器。
func NewCounter() *Counter { return &Counter{next: 1} }

// Allocate 分配下一个序号并推进 nextSeq。保证单调递增、无重复。
func (c *Counter) Allocate() int {
	s := c.next
	c.next++
	return s
}

// Next 返回下一个将要分配的序号（即当前 nextSeq），只读。
func (c *Counter) Next() int { return c.next }

// InGap 判定 s 是否落在空洞区间 (deliveredUpTo, nextSeq)：
// 已分配（s < nextSeq）但尚未投递（s > deliveredUpTo）。
// 补发只允许落在该区间内的空槽位。
func (c *Counter) InGap(s, deliveredUpTo int) bool {
	return deliveredUpTo < s && s < c.next
}
