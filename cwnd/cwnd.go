// Package cwnd 维护拥塞窗口状态与慢启动/拥塞避免增长、丢包减半规则。
// 不依赖其他包。
package cwnd

// State 是拥塞控制阶段。
type State int

const (
	SlowStart State = iota
	CongAvoid
)

func (s State) String() string {
	if s == SlowStart {
		return "SlowStart"
	}
	return "CongAvoid"
}

// Window 是拥塞窗口状态。lastChecked 为非导出计数器，记录最近一次
// OnAck 检查过的 ACK 记录个数：partial 用整数累加而非列表扫描，恒为 1。
type Window struct {
	cwnd, ssthresh, maxCwnd int64
	state                   State
	partial                 int64
	lastChecked             int64
}

// New 构造窗口：cwnd=1、SlowStart、partial=0。参数合法性由上层判定。
func New(ssthresh, maxCwnd int64) *Window {
	return &Window{cwnd: 1, ssthresh: ssthresh, maxCwnd: maxCwnd, state: SlowStart}
}

func (w *Window) Cwnd() int64     { return w.cwnd }
func (w *Window) Ssthresh() int64 { return w.ssthresh }
func (w *Window) MaxCwnd() int64  { return w.maxCwnd }
func (w *Window) State() State    { return w.state }
func (w *Window) Partial() int64  { return w.partial }

// AckPreview 是纯函数：返回应用一次 ACK 后的 (cwnd, state, partial)，不改状态。
func (w *Window) AckPreview() (int64, State, int64) {
	c, s, p := w.cwnd, w.state, w.partial
	switch {
	case s == SlowStart && c < w.ssthresh:
		c++ // 慢启动：每 ACK +1
	case s == SlowStart:
		s, p = CongAvoid, 1 // cwnd >= ssthresh（左闭）切换，本次 ACK 计入 partial
	default: // CongAvoid：加性增，每 cwnd 个 ACK 才 +1
		p++
		if p >= c {
			c, p = c+1, 0
		}
	}
	return c, s, p
}

// ApplyAck 应用一次 ACK 增长，并记录本次检查的 ACK 记录数（1 条）。
func (w *Window) ApplyAck() {
	w.lastChecked = 1
	w.cwnd, w.state, w.partial = w.AckPreview()
}

// AtFloor 报告 cwnd 是否已触底（==1）。
func (w *Window) AtFloor() bool { return w.cwnd == 1 }

// ApplyLoss 丢包：ssthresh=max(cwnd/2,2)（向下取整），cwnd=1，回慢启动。
func (w *Window) ApplyLoss() {
	if s := w.cwnd / 2; s >= 2 {
		w.ssthresh = s
	} else {
		w.ssthresh = 2
	}
	w.cwnd, w.state, w.partial = 1, SlowStart, 0
}

// VerifyAckCost 只回报最近一次 ACK 检查记录数是否 <=1（true/fail），
// 不返回计数器数值：调用方无从读到它，精确断言只由包内测试做。
func (w *Window) VerifyAckCost() bool { return w.lastChecked <= 1 }
