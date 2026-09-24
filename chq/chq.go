// Package chq 单通道 FIFO 队列、记录中标记、通道状态累积、屏障后记录留存。
package chq

// Ch 是单通道状态：输入队列、通道状态、屏障后记录、记录中标记。
type Ch struct {
	queue  []int
	state  []int
	post   []int
	rec    bool
	visits int // 最近一次 Arrive/Step 访问过的已存记录个数（非导出，仅供包内测试）
}

func (c *Ch) Arrive(v int)        { c.queue = append(c.queue, v); c.visits = 1 }
func (c *Ch) StateAppend(v int)   { c.state = append(c.state, v); c.visits++ }
func (c *Ch) PostAppend(v int)    { c.post = append(c.post, v) }
func (c *Ch) Len() int            { return len(c.queue) }
func (c *Ch) StateLen() int       { return len(c.state) }
func (c *Ch) SetRecording(b bool) { c.rec = b }

// Step 弹出队首；空队列返回 ok=false。
func (c *Ch) Step() (v int, ok bool) {
	if len(c.queue) == 0 {
		c.visits = 0
		return 0, false
	}
	v, c.queue = c.queue[0], c.queue[1:]
	c.visits = 1
	return v, true
}

// SnapshotState 把当前队列整体抄为通道状态初始内容（仅检查点开始时调用）。
func (c *Ch) SnapshotState() { c.state = append([]int(nil), c.queue...) }

func (c *Ch) State() []int { return append([]int(nil), c.state...) }
func (c *Ch) Post() []int  { return append([]int(nil), c.post...) }
func (c *Ch) Queue() []int { return append([]int(nil), c.queue...) }

// ClearCheckpoint 检查点完成后清空通道状态与屏障后记录。
func (c *Ch) ClearCheckpoint() { c.state, c.post, c.rec = nil, nil, false }

// Reset 恢复用：以给定队列重建，清空检查点相关状态。
func (c *Ch) Reset(q []int) { c.queue, c.state, c.post, c.rec = q, nil, nil, false }
