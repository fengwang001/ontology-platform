// Package align 跟踪单条输入通道的检查点屏障状态：
// 阻塞标记、当前屏障 id、阻塞期间的在途记录缓冲。
// 它不感知其他通道，也不做跨通道对齐。
package align

// Channel 是一条输入通道的对齐状态，零值即可用（从未见过屏障）。
type Channel struct {
	lastBarrier int   // 本通道最近一次见到的屏障 id（0 表示从未见过）
	barID       int   // 当前阻塞所等的屏障 id；0 表示未阻塞
	pending     []int // 阻塞期间到达、属于下一 epoch 的记录 delta
}

// LastBarrier 返回本通道最近一次见到的屏障 id。
func (c *Channel) LastBarrier() int { return c.lastBarrier }

// Block 标记本通道阻塞于屏障 id，并记录该 id。
// 调用方须先完成 id 合法性校验。
func (c *Channel) Block(id int) {
	c.lastBarrier = id
	c.barID = id
}

// Blocked 报告本通道是否正阻塞于某个屏障。
func (c *Channel) Blocked() bool { return c.barID != 0 }

// BarrierID 返回当前阻塞的屏障 id；未阻塞时为 0。
func (c *Channel) BarrierID() int { return c.barID }

// Buffer 把一条在阻塞期间到达的记录按到达顺序暂存。
func (c *Channel) Buffer(delta int) {
	c.pending = append(c.pending, delta)
}

// Pending 返回在途记录（按到达顺序），不清除缓冲。
func (c *Channel) Pending() []int { return c.pending }

// Drain 取走全部在途记录并清空缓冲，原切片不保留别名。
func (c *Channel) Drain() []int {
	out := c.pending
	c.pending = nil
	return out
}

// Unblock 解除阻塞（在途缓冲由 Drain 另行取走）。
func (c *Channel) Unblock() { c.barID = 0 }
