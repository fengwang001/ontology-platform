// Package clock 提供单调不回退的操作时钟。
package clock

// Clock 记录上一次被接受操作的 now。被拒绝的操作不得推进时钟。
type Clock struct {
	last int64
	set  bool
}

// Acceptable 报告 now 是否可接受：不得小于上一次被接受操作的 now。
func (c *Clock) Acceptable(now int64) bool {
	return !c.set || now >= c.last
}

// Advance 在操作被接受后推进时钟。
func (c *Clock) Advance(now int64) {
	c.last = now
	c.set = true
}

// Last 返回上一次被接受操作的 now，以及是否已有被接受操作。
func (c *Clock) Last() (int64, bool) {
	return c.last, c.set
}
