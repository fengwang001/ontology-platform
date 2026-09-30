package flowcontrol

import "fmt"

// stream 记录单个流的生命周期与窗口记账。
//
// 不变式：window 恒等于按其历史重算之值：
// 打开时初值 + 各次增量 - 已放行字节 + 历次调整差值之和。
type stream struct {
	window int64
	closed bool
}

// Controller 是多流发送端的两级流量控制记账器。
//
// 所有方法均可被并发调用；mu 串行化全部状态变更与查询，
// 日志在临界区内按实际判定顺序输出，保证相同调用序列重放结果完全相同。
type Controller struct {
	mu         chan struct{}
	connWindow int64
	initWindow int64
	maxFrame   int64
	streams    map[int64]*stream
	logger     Logger
	seq        uint64
}

// New 创建记账器：connWindow 为连接窗口初值，initWindow 为初始窗口 I，
// maxFrame 为帧长上限 F。窗口初值为负或超过 MaxWindow、maxFrame 非正则拒绝。
func New(connWindow, initWindow, maxFrame int64, opts ...Option) (*Controller, error) {
	if connWindow < 0 || connWindow > MaxWindow {
		return nil, ErrInvalidWindow
	}
	if initWindow < 0 || initWindow > MaxWindow {
		return nil, ErrInvalidWindow
	}
	if maxFrame <= 0 {
		return nil, ErrInvalidFrame
	}
	c := &Controller{
		mu:         make(chan struct{}, 1),
		connWindow: connWindow,
		initWindow: initWindow,
		maxFrame:   maxFrame,
		streams:    make(map[int64]*stream),
		logger:     noopLogger{},
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.logger == nil {
		c.logger = noopLogger{}
	}
	return c, nil
}

// Option 配置 Controller。
type Option func(*Controller)

// WithLogger 设置调用日志输出。
func WithLogger(l Logger) Option {
	return func(c *Controller) { c.logger = l }
}

// OpenStream 以编号 id 开流，窗口初值取当前 I。编号须为未用过的正整数。
func (c *Controller) OpenStream(id int64) error {
	c.lock()
	defer c.unlock()
	if id <= 0 {
		return c.reject("open", streamID(id), ErrInvalidID, "id=%d 非正", id)
	}
	if _, used := c.streams[id]; used {
		return c.reject("open", streamID(id), ErrIDUsed, "id=%d 已用过（含已关闭流），不可复用", id)
	}
	c.streams[id] = &stream{window: c.initWindow}
	c.allow("open", streamID(id), "以当前初始窗口 I=%d 建立流 %d，未用编号", c.initWindow, id)
	return nil
}

// CloseStream 关闭流。关闭后编号不可复用，未用窗口不回补连接窗口。
func (c *Controller) CloseStream(id int64) error {
	c.lock()
	defer c.unlock()
	s, err := c.lookup("close", id)
	if err != nil {
		return err
	}
	if s.closed {
		return c.reject("close", streamID(id), ErrStreamClosed, "流 %d 已关闭", id)
	}
	s.closed = true
	c.allow("close", streamID(id), "关闭流 %d；其未用窗口 %d 不回补连接窗口（仍为 %d）",
		id, s.window, c.connWindow)
	return nil
}

// Send 在连接窗口与流窗口都够用时放行 n 字节，两处同时扣减；否则全有或全无。
func (c *Controller) Send(id, n int64) error {
	c.lock()
	defer c.unlock()
	// 拒绝顺序只报第一个：不存在 → 已关闭 → n 非正 → n>F → 连接窗口 → 流窗口。
	s, err := c.lookup("send", id)
	if err != nil {
		return err
	}
	if s.closed {
		return c.reject("send", streamID(id), ErrStreamClosed, "流 %d 已关闭", id)
	}
	if n <= 0 {
		return c.reject("send", streamID(id), ErrNonPositive, "n=%d 非正", n)
	}
	if n > c.maxFrame {
		return c.reject("send", streamID(id), ErrFrameTooLarge,
			"n=%d 超过帧长上限 F=%d", n, c.maxFrame)
	}
	if n > c.connWindow {
		return c.reject("send", streamID(id), ErrConnWindow,
			"n=%d 超过连接窗口 %d", n, c.connWindow)
	}
	if n > s.window {
		return c.reject("send", streamID(id), ErrStreamWindow,
			"n=%d 超过流窗口 %d（窗口为负亦归此项）", n, s.window)
	}
	// 全有或全无：两处同时扣减。
	c.connWindow -= n
	s.window -= n
	c.allow("send", streamID(id), "n=%d ≤ F=%d、连接窗口 %d、流窗口 %d；两处同时扣减后连接=%d 流=%d",
		n, c.maxFrame, c.connWindow+n, s.window+n, c.connWindow, s.window)
	return nil
}

// Increment 对连接（id 为 0）或某流增加正数 delta；加后超过上限则拒绝。
func (c *Controller) Increment(id, delta int64) error {
	c.lock()
	defer c.unlock()
	// 拒绝顺序：不存在 → 已关闭 → 增量非正 → 连接窗口溢出 → 流窗口溢出。
	if id == 0 {
		if delta <= 0 {
			return c.reject("increment", connID, ErrNonPositive, "delta=%d 非正", delta)
		}
		if addsOverflow(c.connWindow, delta) {
			return c.reject("increment", connID, ErrConnOverflow,
				"连接窗口 %d + delta=%d 超过上限 %d", c.connWindow, delta, MaxWindow)
		}
		c.connWindow += delta
		c.allow("increment", connID, "连接窗口 %d + %d = %d", c.connWindow-delta, delta, c.connWindow)
		return nil
	}
	s, err := c.lookup("increment", id)
	if err != nil {
		return err
	}
	if s.closed {
		return c.reject("increment", streamID(id), ErrStreamClosed, "流 %d 已关闭", id)
	}
	if delta <= 0 {
		return c.reject("increment", streamID(id), ErrNonPositive, "delta=%d 非正", delta)
	}
	if addsOverflow(s.window, delta) {
		return c.reject("increment", streamID(id), ErrStreamOverflow,
			"流 %d 窗口 %d + delta=%d 超过上限 %d", id, s.window, delta, MaxWindow)
	}
	s.window += delta
	c.allow("increment", streamID(id), "流 %d 窗口 %d + %d = %d",
		id, s.window-delta, delta, s.window)
	return nil
}

// AdjustInitWindow 将初始窗口调整为 next：每个未关闭流的窗口加上 next-I，
// 任一未关闭流调整后超过上限则所有流都不改。
func (c *Controller) AdjustInitWindow(next int64) error {
	c.lock()
	defer c.unlock()
	// 拒绝顺序：next 非法 → 任一未关闭流溢出（预检，整体不改）。
	if next < 0 || next > MaxWindow {
		return c.reject("adjust", systemID, ErrInvalidWindow,
			"I'=%d 为负或超过上限 %d", next, MaxWindow)
	}
	diff := next - c.initWindow
	for id, s := range c.streams {
		if s.closed {
			continue
		}
		if addsOverflow(s.window, diff) {
			return c.reject("adjust", systemID, ErrStreamOverflow,
				"预检：流 %d 调整后窗口 %d + (%d) 超过上限 %d；所有流均不修改，I 保持 %d",
				id, s.window, diff, MaxWindow, c.initWindow)
		}
	}
	for _, s := range c.streams {
		if !s.closed {
			s.window += diff
		}
	}
	old := c.initWindow
	c.initWindow = next
	c.allow("adjust", systemID,
		"I: %d → %d（差值 %d）；所有未关闭流追溯加差值，连接窗口不变=%d；此后新流取 %d；涉及流=%v",
		old, next, diff, c.connWindow, next, openIDs(c.streams))
	return nil
}

// ConnWindow 返回当前连接窗口（按历史重算之值）。
func (c *Controller) ConnWindow() int64 {
	c.lock()
	defer c.unlock()
	c.log("query", connID, "ok", "连接窗口=%d", c.connWindow)
	return c.connWindow
}

// StreamWindow 返回某流当前窗口；流不存在或已关闭时返回错误。
func (c *Controller) StreamWindow(id int64) (int64, error) {
	c.lock()
	defer c.unlock()
	s, err := c.lookup("query", id)
	if err != nil {
		return 0, err
	}
	if s.closed {
		err = ErrStreamClosed
		c.log("query", streamID(id), "reject: "+err.Error(), "流 %d 已关闭", id)
		return 0, err
	}
	c.log("query", streamID(id), "ok", "流 %d 窗口=%d", id, s.window)
	return s.window, nil
}

// StreamClosed 报告流是否已关闭；流不存在时返回错误。
func (c *Controller) StreamClosed(id int64) (bool, error) {
	c.lock()
	defer c.unlock()
	s, err := c.lookup("query", id)
	if err != nil {
		return false, err
	}
	c.log("query", streamID(id), "ok", "流 %d closed=%v", id, s.closed)
	return s.closed, nil
}

// InitWindow 返回当前初始窗口 I（此后新流取此值）。
func (c *Controller) InitWindow() int64 {
	c.lock()
	defer c.unlock()
	c.log("query", systemID, "ok", "当前初始窗口 I=%d", c.initWindow)
	return c.initWindow
}

// --- 内部机制（调用方须持有锁的语义由 lock/unlock 保证） ---

const (
	connID   = int64(0)
	systemID = int64(-1)
)

func streamID(id int64) int64 { return id }

func (c *Controller) lock()   { c.mu <- struct{}{} }
func (c *Controller) unlock() { <-c.mu }

// lookup 按规定顺序判定“不存在 → 已关闭”中的第一项（不存在优先）。
func (c *Controller) lookup(op string, id int64) (*stream, error) {
	s, ok := c.streams[id]
	if !ok {
		return nil, c.reject(op, streamID(id), ErrStreamNotFound, "流 %d 从未开过", id)
	}
	return s, nil
}

// addsOverflow 判定 window+delta 是否超过 MaxWindow（delta 可为负）。
// 以 MaxWindow-window 为阈值，避免加法本身溢出。
func addsOverflow(window, delta int64) bool {
	if delta >= 0 {
		return delta > MaxWindow-window
	}
	return false
}

func openIDs(m map[int64]*stream) []int64 {
	ids := make([]int64, 0, len(m))
	for id, s := range m {
		if !s.closed {
			ids = append(ids, id)
		}
	}
	// 简单插入排序，保证日志确定性。
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j-1] > ids[j]; j-- {
			ids[j-1], ids[j] = ids[j], ids[j-1]
		}
	}
	return ids
}

func (c *Controller) reject(op string, target int64, err error, whyFormat string, args ...any) error {
	c.log(op, target, "reject: "+err.Error(), whyFormat, args...)
	return err
}

func (c *Controller) allow(op string, target int64, whyFormat string, args ...any) {
	c.log(op, target, "ok", whyFormat, args...)
}

func (c *Controller) log(op string, target int64, result, whyFormat string, args ...any) {
	c.seq++
	targetName := "stream"
	switch target {
	case connID:
		targetName = "connection"
	case systemID:
		targetName = "system"
	}
	why := sprintf(whyFormat, args...)
	if target == connID || target == systemID {
		c.logger.Printf("[flowcontrol #%d] op=%s target=%s => %s | %s", c.seq, op, targetName, result, why)
	} else {
		c.logger.Printf("[flowcontrol #%d] op=%s target=%s(%d) => %s | %s", c.seq, op, targetName, target, result, why)
	}
}

func sprintf(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}
