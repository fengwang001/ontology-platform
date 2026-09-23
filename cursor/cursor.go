// Package cursor 管理单个读者的读取位置与掉队判定，依赖 slotring 定位槽位。
package cursor

import "ontology/slotring"

// Status 是读者游标的生命周期状态。
type Status int

const (
	// Active 表示游标正常、可读。
	Active Status = iota
	// Behind 表示游标想要的序号已被覆盖，处于掉队状态。
	Behind
	// Closed 表示游标所属读者已注销。
	Closed
)

// FellBehindError 是可判定的掉队错误，携带错过的条数。
type FellBehindError struct{ Missed int64 }

func (e FellBehindError) Error() string { return "cursor fell behind" }

// Cursor 是单个读者的游标：下一条想读的序号与生命周期状态。
type Cursor struct {
	want   int64
	status Status
}

// New 创建一个下一条想读序号为 want 的游标。
func New(want int64) *Cursor { return &Cursor{want: want, status: Active} }

// Want 返回下一条想读的序号。
func (c *Cursor) Want() int64 { return c.want }

// Status 返回游标状态。
func (c *Cursor) Status() Status { return c.status }

// Slot 借由底层 slotring 返回 want 对应的槽位下标。
func (c *Cursor) Slot(r *slotring.Ring) int { return r.Slot(c.want) }

// IsBehind 在 want 早于最旧可读序号 oldest 时判定掉队。
func (c *Cursor) IsBehind(oldest int64) bool { return c.status == Active && c.want < oldest }

// MarkBehind 把游标标记为掉队。
func (c *Cursor) MarkBehind() { c.status = Behind }

// Missed 返回相对当前最旧可读序号错过的条数。
func (c *Cursor) Missed(oldest int64) int64 { return oldest - c.want }

// Advance 在成功消费序号 seq 后把 want 推进到 seq+1。
func (c *Cursor) Advance(seq int64) { c.want = seq + 1 }

// Recover 把游标重新定位到最旧可读序号 oldest 并恢复为活跃。
func (c *Cursor) Recover(oldest int64) {
	c.want = oldest
	c.status = Active
}

// Close 注销游标。
func (c *Cursor) Close() { c.status = Closed }

// Lag 返回相对最新序号 head 的落后条数；超前（暂无数据）时为 0。
func (c *Cursor) Lag(head int64) int64 {
	if c.want > head {
		return 0
	}
	return head - c.want
}
