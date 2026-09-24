// Package adapt 在 wmline 之上实现观察窗口、迟到计数与 delay 自适应结算。依赖 wmline。
package adapt

import "ontology/wmline"

// Controller 统计窗口内迟到率并按规则调整 delay。用 New 构造。
type Controller struct {
	line *wmline.Line
	step int64
	w    int64
	hi   int64
	lo   int64

	n         int64 // 当前窗口已处理事件数
	lateCount int64 // 当前窗口迟到数，逐事件 O(1) 累加
	checked   int64 // 最近一次结算时检查（回扫）的事件条数；非导出，仅供同包白盒测试
}

// New 构造控制器。调用方保证参数已校验（step>0, W>=1, 0<=lo<hi<=W）。
func New(line *wmline.Line, step, w, hi, lo int64) *Controller {
	return &Controller{line: line, step: step, w: w, hi: hi, lo: lo}
}

// Feed 处理一个事件，返回是否迟到；窗口满 W 条时结算并复位计数。
func (c *Controller) Feed(ts int64) bool {
	late := c.line.Observe(ts)
	c.n++
	if late {
		c.lateCount++
	}
	if c.n == c.w {
		c.settle()
	}
	return late
}

// settle 结算窗口：直接读 lateCount 这个 O(1) 累计值，不回扫窗口内任何事件。
func (c *Controller) settle() {
	c.checked = 1 // 只读取一个累计计数，与窗口大小 W 无关
	d := c.line.Delay()
	switch {
	case c.lateCount >= c.hi:
		c.line.SetDelay(d + c.step)
	case c.lateCount <= c.lo:
		c.line.SetDelay(d - c.step)
	}
	c.n = 0
	c.lateCount = 0
}

// WM 返回当前水位线。
func (c *Controller) WM() int64 { return c.line.WM() }

// Delay 返回当前滞后量。
func (c *Controller) Delay() int64 { return c.line.Delay() }
