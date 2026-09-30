package flowctl

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// logStep prints the input, observed output and the rationale of a check.
func logStep(t *testing.T, input string, output any, rationale string) {
	t.Helper()
	t.Logf("输入: %s | 输出: %v | 判定依据: %s", input, output, rationale)
}

func mustOpen(t *testing.T, c *Controller, id int64) {
	t.Helper()
	if err := c.OpenStream(id); err != nil {
		t.Fatalf("OpenStream(%d) 意外失败: %v", id, err)
	}
}

func streamWindow(t *testing.T, c *Controller, id int64) int64 {
	t.Helper()
	w, err := c.StreamWindow(id)
	if err != nil {
		t.Fatalf("StreamWindow(%d) 意外失败: %v", id, err)
	}
	return w
}

func TestNewValidation(t *testing.T) {
	cases := []struct {
		name                 string
		conn, initial, frame int64
		want                 error
	}{
		{"连接窗口为负", -1, 100, 100, ErrInvalidConnWindow},
		{"连接窗口超限", MaxWindow + 1, 100, 100, ErrInvalidConnWindow},
		{"初始窗口为负", 100, -1, 100, ErrInvalidInitialWindow},
		{"初始窗口超限", 100, MaxWindow + 1, 100, ErrInvalidInitialWindow},
		{"帧长为零", 100, 100, 0, ErrInvalidMaxFrame},
		{"帧长为负", 100, 100, -5, ErrInvalidMaxFrame},
		{"帧长超限", 100, 100, MaxWindow + 1, ErrInvalidMaxFrame},
	}
	for _, tc := range cases {
		_, err := New(tc.conn, tc.initial, tc.frame)
		logStep(t, tc.name, err, "窗口初值须落在 [0, 2^31-1]，帧长须落在 [1, 2^31-1]")
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: 得到 %v，期望 %v", tc.name, err, tc.want)
		}
	}
	if _, err := New(MaxWindow, MaxWindow, MaxWindow); err != nil {
		t.Errorf("边界最大值应可构造，得到 %v", err)
	}
	if _, err := New(0, 0, 1); err != nil {
		t.Errorf("零窗口应可构造，得到 %v", err)
	}
}

func TestOpenAndCloseStream(t *testing.T) {
	c, err := New(1000, 100, 50)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}

	err = c.OpenStream(0)
	logStep(t, "OpenStream(0)", err, "编号非正应拒绝")
	if !errors.Is(err, ErrInvalidStreamID) {
		t.Errorf("得到 %v，期望 %v", err, ErrInvalidStreamID)
	}
	err = c.OpenStream(-3)
	logStep(t, "OpenStream(-3)", err, "编号非正应拒绝")
	if !errors.Is(err, ErrInvalidStreamID) {
		t.Errorf("得到 %v，期望 %v", err, ErrInvalidStreamID)
	}

	mustOpen(t, c, 1)
	if w := streamWindow(t, c, 1); w != 100 {
		t.Errorf("新流窗口 = %d，期望取当时初始窗口 100", w)
	}

	err = c.OpenStream(1)
	logStep(t, "OpenStream(1) 重复", err, "编号已用过应拒绝")
	if !errors.Is(err, ErrStreamIDUsed) {
		t.Errorf("得到 %v，期望 %v", err, ErrStreamIDUsed)
	}

	if err := c.CloseStream(1); err != nil {
		t.Fatalf("CloseStream(1) 失败: %v", err)
	}
	err = c.OpenStream(1)
	logStep(t, "关闭后 OpenStream(1)", err, "关闭后编号不可复用")
	if !errors.Is(err, ErrStreamIDUsed) {
		t.Errorf("得到 %v，期望 %v", err, ErrStreamIDUsed)
	}

	err = c.CloseStream(1)
	logStep(t, "重复 CloseStream(1)", err, "已关闭的流应报已关闭")
	if !errors.Is(err, ErrStreamClosed) {
		t.Errorf("得到 %v，期望 %v", err, ErrStreamClosed)
	}
	err = c.CloseStream(99)
	logStep(t, "CloseStream(99)", err, "不存在的流应报不存在")
	if !errors.Is(err, ErrStreamNotFound) {
		t.Errorf("得到 %v，期望 %v", err, ErrStreamNotFound)
	}
}

func TestSendRejectionOrder(t *testing.T) {
	c, err := New(60, 30, 100)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	mustOpen(t, c, 1)
	mustOpen(t, c, 2)
	if err := c.CloseStream(2); err != nil {
		t.Fatalf("CloseStream(2) 失败: %v", err)
	}

	cases := []struct {
		name      string
		id, n     int64
		want      error
		rationale string
	}{
		{"流不存在", 9, 5, ErrStreamNotFound, "拒绝顺序第 1 位：流不存在"},
		{"流已关闭", 2, 5, ErrStreamClosed, "拒绝顺序第 2 位：流已关闭"},
		{"n 为零", 1, 0, ErrNonPositiveAmount, "拒绝顺序第 3 位：n 非正"},
		{"n 为负", 1, -4, ErrNonPositiveAmount, "拒绝顺序第 3 位：n 非正"},
		{"n 超过帧长上限", 1, 101, ErrExceedsMaxFrame, "拒绝顺序第 4 位：n 超过 F"},
		{"n 超过连接窗口", 1, 61, ErrExceedsConnWindow, "拒绝顺序第 5 位：n 超过连接窗口"},
		{"n 超过流窗口", 1, 40, ErrExceedsStreamWindow, "拒绝顺序第 6 位：n 超过流窗口"},
	}
	for _, tc := range cases {
		err := c.Send(tc.id, tc.n)
		logStep(t, "Send("+itoa(tc.id)+", "+itoa(tc.n)+")", err, tc.rationale)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: 得到 %v，期望 %v", tc.name, err, tc.want)
		}
	}

	// 连接窗口与流窗口分别触发的场景。
	c2, _ := New(25, 40, 100)
	mustOpen(t, c2, 1)
	err = c2.Send(1, 30)
	logStep(t, "Send(1, 30) 连接窗口 25 流窗口 40", err, "n 超过连接窗口应先于流窗口报出")
	if !errors.Is(err, ErrExceedsConnWindow) {
		t.Errorf("得到 %v，期望 %v", err, ErrExceedsConnWindow)
	}
	err = c2.Send(1, 20)
	logStep(t, "Send(1, 20)", err, "两级窗口均够，应放行并同时扣减")
	if err != nil {
		t.Errorf("意外拒绝: %v", err)
	}
	if w := c2.ConnWindow(); w != 5 {
		t.Errorf("连接窗口 = %d，期望 5", w)
	}
	if w := streamWindow(t, c2, 1); w != 20 {
		t.Errorf("流窗口 = %d，期望 20", w)
	}
	err = c2.Send(1, 10)
	logStep(t, "Send(1, 10) 连接窗口 5 流窗口 20", err, "连接窗口先耗尽，报超过连接窗口")
	if !errors.Is(err, ErrExceedsConnWindow) {
		t.Errorf("得到 %v，期望 %v", err, ErrExceedsConnWindow)
	}
}

func itoa(v int64) string {
	return fmt.Sprintf("%d", v)
}

func TestAdjustInitialNegativeWindow(t *testing.T) {
	c, err := New(1000, 100, 1000)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	mustOpen(t, c, 1)
	mustOpen(t, c, 2)
	if err := c.Send(1, 60); err != nil {
		t.Fatalf("Send(1, 60) 失败: %v", err)
	}
	// 流 1 窗口 40，流 2 窗口 100；I: 100 -> 20，差值 -80。
	err = c.AdjustInitial(20)
	logStep(t, "AdjustInitial(20)，I 由 100 下调", err, "未关闭流窗口追溯加 I'-I=-80，允许变负")
	if err != nil {
		t.Fatalf("AdjustInitial(20) 失败: %v", err)
	}
	if w := streamWindow(t, c, 1); w != -40 {
		t.Errorf("流 1 窗口 = %d，期望 -40", w)
	}
	if w := streamWindow(t, c, 2); w != 20 {
		t.Errorf("流 2 窗口 = %d，期望 20", w)
	}
	if w := c.ConnWindow(); w != 940 {
		t.Errorf("连接窗口 = %d，期望 940（调整不影响连接窗口）", w)
	}
	if got := c.InitialWindow(); got != 20 {
		t.Errorf("当前初始窗口 = %d，期望 20", got)
	}

	err = c.Send(1, 1)
	logStep(t, "Send(1, 1) 流窗口为 -40", err, "窗口为负归入「超过流窗口」")
	if !errors.Is(err, ErrExceedsStreamWindow) {
		t.Errorf("得到 %v，期望 %v", err, ErrExceedsStreamWindow)
	}

	// 增量补正：-40 + 41 = 1，恰好可发 1 字节。
	if err := c.Increment(1, 41); err != nil {
		t.Fatalf("Increment(1, 41) 失败: %v", err)
	}
	if w := streamWindow(t, c, 1); w != 1 {
		t.Errorf("补正后流 1 窗口 = %d，期望 1", w)
	}
	err = c.Send(1, 1)
	logStep(t, "Increment(1, 41) 后 Send(1, 1)", err, "补正后恰好可发，两处同时扣减")
	if err != nil {
		t.Errorf("意外拒绝: %v", err)
	}
	if w := streamWindow(t, c, 1); w != 0 {
		t.Errorf("发送后流 1 窗口 = %d，期望 0", w)
	}
	if w := c.ConnWindow(); w != 939 {
		t.Errorf("发送后连接窗口 = %d，期望 939", w)
	}

	// 此后新流取调整后的 I'=20。
	mustOpen(t, c, 3)
	if w := streamWindow(t, c, 3); w != 20 {
		t.Errorf("调整后新流窗口 = %d，期望 20", w)
	}
}

func TestAdjustInitialOverflowRejectsAll(t *testing.T) {
	c, err := New(1000, 100, 1000)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	mustOpen(t, c, 1)
	mustOpen(t, c, 2)
	if err := c.Increment(1, MaxWindow-150); err != nil {
		t.Fatalf("Increment 失败: %v", err)
	}
	// 流 1 窗口 = MaxWindow-50，流 2 窗口 = 100。
	// I: 100 -> 200，差值 +100，流 1 调整后为 MaxWindow+50，溢出。
	before1 := streamWindow(t, c, 1)
	before2 := streamWindow(t, c, 2)
	err = c.AdjustInitial(200)
	logStep(t, "AdjustInitial(200) 流 1 将溢出", err, "任一未关闭流调整后超限则整体拒绝，所有流都不改")
	if !errors.Is(err, ErrAdjustWindowOverflow) {
		t.Errorf("得到 %v，期望 %v", err, ErrAdjustWindowOverflow)
	}
	if w := streamWindow(t, c, 1); w != before1 {
		t.Errorf("流 1 窗口被改为 %d，期望保持 %d", w, before1)
	}
	if w := streamWindow(t, c, 2); w != before2 {
		t.Errorf("流 2 窗口被改为 %d，期望保持 %d", w, before2)
	}
	if got := c.InitialWindow(); got != 100 {
		t.Errorf("初始窗口被改为 %d，期望保持 100", got)
	}

	err = c.AdjustInitial(MaxWindow + 1)
	logStep(t, "AdjustInitial(MaxWindow+1)", err, "I' 超限应拒绝")
	if !errors.Is(err, ErrInvalidInitialWindow) {
		t.Errorf("得到 %v，期望 %v", err, ErrInvalidInitialWindow)
	}
	err = c.AdjustInitial(-1)
	logStep(t, "AdjustInitial(-1)", err, "I' 为负应拒绝")
	if !errors.Is(err, ErrInvalidInitialWindow) {
		t.Errorf("得到 %v，期望 %v", err, ErrInvalidInitialWindow)
	}
}

func TestIncrementRules(t *testing.T) {
	c, err := New(100, 50, 1000)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	mustOpen(t, c, 1)
	mustOpen(t, c, 2)
	if err := c.CloseStream(2); err != nil {
		t.Fatalf("CloseStream(2) 失败: %v", err)
	}

	cases := []struct {
		name      string
		id, delta int64
		want      error
		rationale string
	}{
		{"流不存在", 9, 10, ErrStreamNotFound, "增量拒绝顺序第 1 位：流不存在"},
		{"流已关闭", 2, 10, ErrStreamClosed, "增量拒绝顺序第 2 位：流已关闭"},
		{"增量为零", 1, 0, ErrNonPositiveAmount, "增量拒绝顺序第 3 位：增量非正"},
		{"增量为负", 1, -5, ErrNonPositiveAmount, "增量拒绝顺序第 3 位：增量非正"},
		{"流窗口溢出", 1, MaxWindow, ErrStreamWindowOverflow, "加后超过 2^31-1 应拒绝"},
		{"连接增量非正", ConnID, 0, ErrNonPositiveAmount, "连接窗口增量同样须为正"},
		{"连接窗口溢出", ConnID, MaxWindow, ErrConnWindowOverflow, "连接窗口加后超限应拒绝"},
	}
	for _, tc := range cases {
		err := c.Increment(tc.id, tc.delta)
		logStep(t, "Increment("+itoa(tc.id)+", "+itoa(tc.delta)+")", err, tc.rationale)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: 得到 %v，期望 %v", tc.name, err, tc.want)
		}
	}

	// 全部被拒绝，状态不变。
	if w := c.ConnWindow(); w != 100 {
		t.Errorf("连接窗口 = %d，期望保持 100", w)
	}
	if w := streamWindow(t, c, 1); w != 50 {
		t.Errorf("流 1 窗口 = %d，期望保持 50", w)
	}

	if err := c.Increment(ConnID, 40); err != nil {
		t.Fatalf("Increment(ConnID, 40) 失败: %v", err)
	}
	if w := c.ConnWindow(); w != 140 {
		t.Errorf("连接窗口 = %d，期望 140", w)
	}
	if err := c.Increment(1, 10); err != nil {
		t.Fatalf("Increment(1, 10) 失败: %v", err)
	}
	if w := streamWindow(t, c, 1); w != 60 {
		t.Errorf("流 1 窗口 = %d，期望 60", w)
	}
}

func TestConnWindowExhaustedFirst(t *testing.T) {
	c, err := New(30, 100, 1000)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	mustOpen(t, c, 1)
	mustOpen(t, c, 2)
	if err := c.Send(1, 20); err != nil {
		t.Fatalf("Send(1, 20) 失败: %v", err)
	}
	if err := c.Send(2, 10); err != nil {
		t.Fatalf("Send(2, 10) 失败: %v", err)
	}
	// 连接窗口已耗尽为 0，两条流窗口仍各剩 80/90。
	err = c.Send(1, 1)
	logStep(t, "Send(1, 1) 连接窗口 0 流窗口 80", err, "连接窗口先耗尽，报超过连接窗口")
	if !errors.Is(err, ErrExceedsConnWindow) {
		t.Errorf("得到 %v，期望 %v", err, ErrExceedsConnWindow)
	}
	if w := c.ConnWindow(); w != 0 {
		t.Errorf("连接窗口 = %d，期望 0", w)
	}
}

func TestStreamWindowExhaustedFirst(t *testing.T) {
	c, err := New(1000, 25, 1000)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	mustOpen(t, c, 1)
	if err := c.Send(1, 25); err != nil {
		t.Fatalf("Send(1, 25) 失败: %v", err)
	}
	// 流窗口耗尽为 0，连接窗口仍剩 975。
	err = c.Send(1, 1)
	logStep(t, "Send(1, 1) 连接窗口 975 流窗口 0", err, "流窗口先耗尽，报超过流窗口")
	if !errors.Is(err, ErrExceedsStreamWindow) {
		t.Errorf("得到 %v，期望 %v", err, ErrExceedsStreamWindow)
	}
	if w := c.ConnWindow(); w != 975 {
		t.Errorf("连接窗口 = %d，期望 975", w)
	}
}

func TestClosedStreamWindowNotRefunded(t *testing.T) {
	c, err := New(100, 40, 1000)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	mustOpen(t, c, 1)
	if err := c.Send(1, 10); err != nil {
		t.Fatalf("Send(1, 10) 失败: %v", err)
	}
	// 连接窗口 90，流 1 窗口 30。关闭流 1，未用的 30 不应回补连接窗口。
	if err := c.CloseStream(1); err != nil {
		t.Fatalf("CloseStream(1) 失败: %v", err)
	}
	w := c.ConnWindow()
	logStep(t, "CloseStream(1) 后 ConnWindow()", w, "关闭流的未用窗口不回补连接窗口")
	if w != 90 {
		t.Errorf("连接窗口 = %d，期望 90", w)
	}
	err = c.Send(1, 1)
	logStep(t, "Send(1, 1) 于已关闭流", err, "已关闭流不可再发送")
	if !errors.Is(err, ErrStreamClosed) {
		t.Errorf("得到 %v，期望 %v", err, ErrStreamClosed)
	}
}

func TestConcurrentSendsNeverExceedConnWindow(t *testing.T) {
	const connInit = 1000
	c, err := New(connInit, connInit, 100)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	const streams = 8
	for id := int64(1); id <= streams; id++ {
		mustOpen(t, c, id)
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	total := int64(0)
	for id := int64(1); id <= streams; id++ {
		for k := 0; k < 50; k++ {
			wg.Add(1)
			go func(id int64) {
				defer wg.Done()
				if err := c.Send(id, 10); err == nil {
					mu.Lock()
					total += 10
					mu.Unlock()
				}
			}(id)
		}
	}
	wg.Wait()

	left := c.ConnWindow()
	logStep(t, "8 流 x 50 协程各发 10 字节，连接窗口初值 1000",
		fmt.Sprintf("放行总量 %d，剩余连接窗口 %d", total, left),
		"并发发送总量不得超过连接窗口，且总量+剩余=初值")
	if total > connInit {
		t.Errorf("并发放行总量 %d 超过连接窗口初值 %d", total, connInit)
	}
	if total+left != connInit {
		t.Errorf("总量 %d + 剩余 %d != 初值 %d", total, left, connInit)
	}
	if total != connInit {
		t.Errorf("总量 %d，期望恰好耗尽 %d", total, connInit)
	}
}

func TestReplayDeterminism(t *testing.T) {
	run := func() string {
		c, err := New(500, 100, 60)
		if err != nil {
			t.Fatalf("New 失败: %v", err)
		}
		out := ""
		record := func(format string, args ...any) {
			out += fmt.Sprintf(format+"\n", args...)
		}
		record("open1=%v", c.OpenStream(1))
		record("open2=%v", c.OpenStream(2))
		record("send1_50=%v", c.Send(1, 50))
		record("send2_70=%v", c.Send(2, 70))
		record("inc0_30=%v", c.Increment(ConnID, 30))
		record("inc2_15=%v", c.Increment(2, 15))
		record("adj80=%v", c.AdjustInitial(80))
		record("send2_70=%v", c.Send(2, 70))
		record("close1=%v", c.CloseStream(1))
		record("send1_1=%v", c.Send(1, 1))
		w1, e1 := c.StreamWindow(1)
		w2, e2 := c.StreamWindow(2)
		record("w1=%d(%v) w2=%d(%v) conn=%d I=%d", w1, e1, w2, e2, c.ConnWindow(), c.InitialWindow())
		return out
	}
	first := run()
	second := run()
	logStep(t, "同一调用序列执行两遍", "两次输出一致", "相同调用序列重放结果完全相同")
	if first != second {
		t.Errorf("重放结果不一致:\n--- 第一遍 ---\n%s\n--- 第二遍 ---\n%s", first, second)
	}
	t.Logf("重放轨迹:\n%s", first)
}
