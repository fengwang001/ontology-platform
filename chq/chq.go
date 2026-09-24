// Package chq 实现单通道 FIFO 队列、记录中标记、通道状态累积与屏障后记录留存。
package chq

// Ch 是单通道状态：待处理队列、进行中检查点的通道状态、全部到达日志。
type Ch struct {
	queue  []int // 未处理记录，head 之前为已处理
	head   int
	state  []int // 进行中检查点的通道状态
	log    []int // 全部到达记录；屏障后留存 = log[mark:]
	rec    bool  // 记录中：Push 同时追加进 state
	mark   int   // 本通道最近一次屏障时已到达条数
	cstate []int // 最近完成检查点的通道状态
	cmark  int   // 最近完成检查点本通道屏障时的 log 长度
	access int   // 最近一次 Push/Pop 访问的已存记录个数（非导出）
}

// New 返回空通道。
func New() *Ch { return &Ch{} }

// Push 追加一条到队尾；记录中时同时累积进通道状态。O(1)，不扫描不拷贝。
func (c *Ch) Push(v int) {
	c.access = 1
	c.log = append(c.log, v)
	c.queue = append(c.queue, v)
	if c.rec {
		c.state = append(c.state, v)
	}
}

// Pop 取出队首；空队列返回 false。O(1)，只移动下标。
func (c *Ch) Pop() (int, bool) {
	c.access = 1
	if c.head >= len(c.queue) {
		return 0, false
	}
	v := c.queue[c.head]
	c.head++
	return v, true
}

// Pending 返回队列中未处理记录数。
func (c *Ch) Pending() int { return len(c.queue) - c.head }

// StateLen 返回进行中检查点的通道状态条数。
func (c *Ch) StateLen() int { return len(c.state) }

// Begin 在检查点首个屏障时调用：以当前队列内容初始化通道状态并开始记录。
func (c *Ch) Begin() {
	c.state = append([]int(nil), c.queue[c.head:]...)
	c.rec = true
}

// Barrier 本通道屏障到达：停止记录，记下屏障位置。
func (c *Ch) Barrier() {
	c.rec = false
	c.mark = len(c.log)
}

// Commit 检查点完成：固化通道状态与屏障位置供恢复使用。
func (c *Ch) Commit() {
	c.cstate = append([]int(nil), c.state...)
	c.cmark = c.mark
}

// Restore 重建队列 = 已完成检查点通道状态 + 屏障后到达的全部记录（原顺序）。
// 无完成检查点时 cstate 为空、cmark 为 0，队列 = 全部已到达记录。
func (c *Ch) Restore() {
	q := make([]int, 0, len(c.cstate)+len(c.log)-c.cmark)
	q = append(q, c.cstate...)
	q = append(q, c.log[c.cmark:]...)
	c.queue, c.head = q, 0
}

// Committed 返回最近完成检查点的通道状态副本。
func (c *Ch) Committed() []int { return append([]int(nil), c.cstate...) }
