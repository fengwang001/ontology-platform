package flowcontrol

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// testLogger 同时把日志写入内存缓冲（供断言）与 testing 输出（供人工查看）。
type testLogger struct {
	mu  sync.Mutex
	buf bytes.Buffer
	t   *testing.T
}

func newTestLogger(t *testing.T) *testLogger {
	t.Helper()
	return &testLogger{t: t}
}

func (l *testLogger) Printf(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	l.mu.Lock()
	l.buf.WriteString(line)
	l.buf.WriteByte('\n')
	l.mu.Unlock()
	l.t.Log(line)
}

func (l *testLogger) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertWindow(t *testing.T, c *Controller, id, want int64) {
	t.Helper()
	got, err := c.StreamWindow(id)
	if err != nil {
		t.Fatalf("StreamWindow(%d): %v", id, err)
	}
	if got != want {
		t.Fatalf("stream %d window=%d, want %d", id, got, want)
	}
}

func TestNewRejects(t *testing.T) {
	cases := []struct {
		name                  string
		conn, initW, maxFrame int64
		wantErr               error
	}{
		{"连接窗口为负", -1, 10, 100, ErrInvalidWindow},
		{"连接窗口超限", MaxWindow + 1, 10, 100, ErrInvalidWindow},
		{"初始窗口为负", 100, -1, 100, ErrInvalidWindow},
		{"初始窗口超限", 100, MaxWindow + 1, 100, ErrInvalidWindow},
		{"帧上限为零", 100, 10, 0, ErrInvalidFrame},
		{"帧上限为负", 100, 10, -5, ErrInvalidFrame},
		{"恰为上限可构造", MaxWindow, MaxWindow, 1, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.conn, tc.initW, tc.maxFrame)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("New(%d,%d,%d) err=%v, want %v", tc.conn, tc.initW, tc.maxFrame, err, tc.wantErr)
			}
		})
	}
}

// 初始窗口下调使流窗口变负；增量补正后恰好可发 F 字节。
func TestAdjustDownNegativeWindowThenIncrementExact(t *testing.T) {
	log := newTestLogger(t)
	c, err := New(100, 10, 4, WithLogger(log))
	if err != nil {
		t.Fatal(err)
	}
	must(t, c.OpenStream(1))

	// I 10 -> 20，流窗口 10+10=20；发掉 5 个 F=4 后流窗口归 0，连接窗口 80。
	must(t, c.AdjustInitWindow(20))
	for range 5 {
		must(t, c.Send(1, 4))
	}
	assertWindow(t, c, 1, 0)

	// I 20 -> 5，差值 -15：流窗口变为 -15；连接窗口不变 80。
	must(t, c.AdjustInitWindow(5))
	assertWindow(t, c, 1, -15)
	if got := c.ConnWindow(); got != 80 {
		t.Fatalf("连接窗口应不受调整影响，got=%d want=80", got)
	}
	// 窗口为负时发送归为“超过流窗口”（即使连接窗口充足、n<=F）。
	if err := c.Send(1, 4); !errors.Is(err, ErrStreamWindow) {
		t.Fatalf("负窗口发送 err=%v, want ErrStreamWindow", err)
	}
	// 增量补正：-15+18=3 仍差 1 不可发；再补 1 恰好可发 F=4。
	must(t, c.Increment(1, 18))
	assertWindow(t, c, 1, 3)
	if err := c.Send(1, 4); !errors.Is(err, ErrStreamWindow) {
		t.Fatalf("补正差 1 时发送 err=%v, want ErrStreamWindow", err)
	}
	must(t, c.Increment(1, 1))
	assertWindow(t, c, 1, 4)
	must(t, c.Send(1, 4))
	assertWindow(t, c, 1, 0)
	if got := c.ConnWindow(); got != 76 {
		t.Fatalf("放行后连接窗口 got=%d want=76", got)
	}

	// 调整后新开的流取新的 I=5。
	must(t, c.OpenStream(2))
	assertWindow(t, c, 2, 5)

	out := log.String()
	for _, want := range []string{"op=adjust", "差值 -15", "op=send", "insufficient stream window", "op=increment"} {
		if !strings.Contains(out, want) {
			t.Fatalf("日志缺少 %q:\n%s", want, out)
		}
	}
}

// 调整致某流溢出时整体不改：所有流窗口、I、新流行为均保持调整前。
func TestAdjustOverflowChangesNothing(t *testing.T) {
	c, _ := New(1000, 10, 100)
	must(t, c.OpenStream(1))
	must(t, c.OpenStream(2))
	must(t, c.Increment(2, MaxWindow-10)) // 流2窗口 = MaxWindow

	// I 10 -> 11：流2 将变为 MaxWindow+1，预检失败，所有流都不改。
	err := c.AdjustInitWindow(11)
	if !errors.Is(err, ErrStreamOverflow) {
		t.Fatalf("err=%v, want ErrStreamOverflow", err)
	}
	assertWindow(t, c, 1, 10)
	assertWindow(t, c, 2, MaxWindow)
	if got := c.InitWindow(); got != 10 {
		t.Fatalf("I 不应改变，got=%d want=10", got)
	}
	must(t, c.OpenStream(3)) // 被拒后新流仍取旧 I=10
	assertWindow(t, c, 3, 10)

	// 下调到 0 不溢出：未关闭流追溯减 10；已关闭流不受影响。
	must(t, c.CloseStream(3))
	must(t, c.AdjustInitWindow(0))
	assertWindow(t, c, 1, 0)
	assertWindow(t, c, 2, MaxWindow-10)
	closed, err := c.StreamClosed(3)
	if err != nil || !closed {
		t.Fatalf("流3 应仍关闭 closed=%v err=%v", closed, err)
	}
	if _, err := c.StreamWindow(3); !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("已关闭流查询窗口 err=%v, want ErrStreamClosed", err)
	}
}

// 连接窗口先耗尽：流窗口尚足，连接窗口不足时拒绝，且不扣任何窗口。
func TestConnectionWindowExhaustedFirst(t *testing.T) {
	c, _ := New(10, 100, 4)
	must(t, c.OpenStream(1))
	must(t, c.Send(1, 4))
	must(t, c.Send(1, 4)) // 连接窗口 2、流窗口 92
	if err := c.Send(1, 4); !errors.Is(err, ErrConnWindow) {
		t.Fatalf("err=%v, want ErrConnWindow", err)
	}
	if got := c.ConnWindow(); got != 2 {
		t.Fatalf("被拒操作不得扣连接窗口，got=%d", got)
	}
	assertWindow(t, c, 1, 92)

	must(t, c.Increment(0, 2)) // 给连接窗口补 2 后恰好可发
	must(t, c.Send(1, 4))
	if got := c.ConnWindow(); got != 0 {
		t.Fatalf("连接窗口应耗尽为0，got=%d", got)
	}
	assertWindow(t, c, 1, 88)
}

// 流窗口先耗尽：连接窗口尚足，流窗口不足时拒绝，且不扣任何窗口。
func TestStreamWindowExhaustedFirst(t *testing.T) {
	c, _ := New(1000, 10, 4)
	must(t, c.OpenStream(1))
	must(t, c.Send(1, 4))
	must(t, c.Send(1, 4)) // 连接 992、流 2
	if err := c.Send(1, 4); !errors.Is(err, ErrStreamWindow) {
		t.Fatalf("err=%v, want ErrStreamWindow", err)
	}
	if got := c.ConnWindow(); got != 992 {
		t.Fatalf("被拒操作不得扣连接窗口，got=%d", got)
	}
	assertWindow(t, c, 1, 2)
}

// 关闭流不回补连接窗口，编号不可复用。
func TestCloseNoRefundAndIDNotReused(t *testing.T) {
	c, _ := New(100, 10, 4)
	must(t, c.OpenStream(1))
	must(t, c.Send(1, 4)) // 连接 96、流 6
	must(t, c.CloseStream(1))
	if got := c.ConnWindow(); got != 96 {
		t.Fatalf("关闭不得回补未用窗口，连接窗口 got=%d want=96", got)
	}
	if err := c.Send(1, 1); !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("关闭流发送 err=%v want ErrStreamClosed", err)
	}
	if err := c.Increment(1, 1); !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("关闭流增量 err=%v want ErrStreamClosed", err)
	}
	if err := c.CloseStream(1); !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("重复关闭 err=%v want ErrStreamClosed", err)
	}
	if err := c.OpenStream(1); !errors.Is(err, ErrIDUsed) {
		t.Fatalf("编号复用 err=%v want ErrIDUsed", err)
	}
	if got := c.ConnWindow(); got != 96 {
		t.Fatalf("被拒操作后连接窗口 got=%d want=96", got)
	}
}

// 发送拒绝顺序：只报第一个。
func TestSendRejectOrder(t *testing.T) {
	c, _ := New(100, 10, 4)
	must(t, c.OpenStream(1))
	must(t, c.CloseStream(1))
	must(t, c.OpenStream(2))

	check := func(name string, do func() error, want error) {
		t.Helper()
		if err := do(); !errors.Is(err, want) {
			t.Fatalf("%s: err=%v want %v", name, err, want)
		}
	}
	check("流不存在", func() error { return c.Send(999, 1) }, ErrStreamNotFound)
	check("流已关闭", func() error { return c.Send(1, 1) }, ErrStreamClosed)
	check("n 非正", func() error { return c.Send(2, 0) }, ErrNonPositive)
	check("n 超 F", func() error { return c.Send(2, 5) }, ErrFrameTooLarge)

	small, _ := New(3, 100, 4) // 连接窗口不足优先于流窗口不足
	must(t, small.OpenStream(7))
	check("连接窗口优先", func() error { return small.Send(7, 4) }, ErrConnWindow)
}

// 增量规则：不存在/已关闭/非正/连接溢出/流溢出；成功路径。
func TestIncrementRules(t *testing.T) {
	c, _ := New(MaxWindow, MaxWindow, 10)
	must(t, c.OpenStream(1))
	must(t, c.OpenStream(2))
	must(t, c.CloseStream(2))

	if err := c.Increment(99, 1); !errors.Is(err, ErrStreamNotFound) {
		t.Fatalf("不存在流 err=%v", err)
	}
	if err := c.Increment(2, 1); !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("已关闭流 err=%v", err)
	}
	if err := c.Increment(0, 0); !errors.Is(err, ErrNonPositive) {
		t.Fatalf("连接零增量 err=%v", err)
	}
	if err := c.Increment(1, -1); !errors.Is(err, ErrNonPositive) {
		t.Fatalf("流负增量 err=%v", err)
	}
	if err := c.Increment(0, 1); !errors.Is(err, ErrConnOverflow) {
		t.Fatalf("连接溢出 err=%v", err)
	}
	if err := c.Increment(1, 1); !errors.Is(err, ErrStreamOverflow) {
		t.Fatalf("流溢出 err=%v", err)
	}
	if got := c.ConnWindow(); got != MaxWindow {
		t.Fatalf("连接窗口被溢出操作改变: %d", got)
	}
	assertWindow(t, c, 1, MaxWindow)

	zero, _ := New(0, 0, 10) // 合法增量
	must(t, zero.OpenStream(1))
	must(t, zero.Increment(0, 100))
	must(t, zero.Increment(1, 50))
	if got := zero.ConnWindow(); got != 100 {
		t.Fatalf("连接窗口 got=%d want=100", got)
	}
	assertWindow(t, zero, 1, 50)
}

// 开流拒绝：编号非正、已用过（含已关闭）。
func TestOpenRejects(t *testing.T) {
	c, _ := New(100, 10, 10)
	if err := c.OpenStream(0); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("id=0 err=%v", err)
	}
	if err := c.OpenStream(-3); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("id=-3 err=%v", err)
	}
	must(t, c.OpenStream(5))
	must(t, c.CloseStream(5))
	if err := c.OpenStream(5); !errors.Is(err, ErrIDUsed) {
		t.Fatalf("复用已关闭编号 err=%v want ErrIDUsed", err)
	}
}

// 并发发送：放行总量绝不超过连接窗口，且每个流不超过其流窗口。
func TestConcurrentSendTotalBounded(t *testing.T) {
	const (
		goroutines = 64
		tries      = 200
		frame      = int64(4)
		connCap    = int64(1000)
	)
	c, _ := New(connCap, connCap, frame)
	for id := int64(1); id <= 4; id++ {
		must(t, c.OpenStream(id))
	}

	var wg sync.WaitGroup
	var acceptedTotal int64
	var acceptedMu sync.Mutex
	perStream := make([]int64, 5)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			local := int64(0)
			localPer := make([]int64, 5)
			for i := 0; i < tries; i++ {
				id := int64((seed+i)%4 + 1)
				if err := c.Send(id, frame); err == nil {
					local += frame
					localPer[id] += frame
				}
			}
			acceptedMu.Lock()
			acceptedTotal += local
			for id := int64(1); id <= 4; id++ {
				perStream[id] += localPer[id]
			}
			acceptedMu.Unlock()
		}(g)
	}

	// 并发混入查询、增量、开流、关流与调整，验证全程线程安全。
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = c.ConnWindow()
		_, _ = c.StreamWindow(1)
		_ = c.Increment(0, 0)      // 非正增量，必被拒且不改状态
		_ = c.AdjustInitWindow(-1) // 非法调整，必被拒且不改状态
	}()

	wg.Wait()

	if acceptedTotal > connCap {
		t.Fatalf("并发放行总量 %d 超过连接窗口 %d", acceptedTotal, connCap)
	}
	if got := c.ConnWindow(); got != connCap-acceptedTotal {
		t.Fatalf("连接窗口重算不符: got=%d want=%d", got, connCap-acceptedTotal)
	}
	for id := int64(1); id <= 4; id++ {
		if perStream[id] > connCap {
			t.Fatalf("流 %d 放行 %d 超过其窗口", id, perStream[id])
		}
		w, err := c.StreamWindow(id)
		if err != nil {
			t.Fatalf("流 %d 查询: %v", id, err)
		}
		if w != connCap-perStream[id] {
			t.Fatalf("流 %d 窗口重算不符: got=%d want=%d", id, w, connCap-perStream[id])
		}
	}
}

// 确定性重放：相同调用序列（含交错结果收集）两次执行，返回结果序列完全相同。
func TestDeterministicReplay(t *testing.T) {
	play := func() string {
		var sb strings.Builder
		log := newTestLogger(t)
		c, _ := New(50, 20, 8, WithLogger(log))
		ids := []int64{1, 2, -1, 1, 3, 0, 2}
		for _, id := range ids {
			fmt.Fprintf(&sb, "open(%d)=%v\n", id, c.OpenStream(id))
		}
		sends := [][2]int64{{1, 8}, {1, 9}, {2, 0}, {2, 8}, {9, 1}, {1, 8}, {2, 8}, {2, 8}}
		for _, s := range sends {
			fmt.Fprintf(&sb, "send(%d,%d)=%v\n", s[0], s[1], c.Send(s[0], s[1]))
		}
		for _, next := range []int64{MaxWindow, 0, -5, 5} {
			fmt.Fprintf(&sb, "adjust(%d)=%v\n", next, c.AdjustInitWindow(next))
		}
		for _, args := range [][2]int64{{0, 100}, {1, -1}, {2, 1}, {3, 1}} {
			fmt.Fprintf(&sb, "inc(%d,%d)=%v\n", args[0], args[1], c.Increment(args[0], args[1]))
		}
		fmt.Fprintf(&sb, "close(2)=%v\n", c.CloseStream(2))
		fmt.Fprintf(&sb, "send(2,1)=%v\n", c.Send(2, 1))
		fmt.Fprintf(&sb, "conn=%d i=%d\n", c.ConnWindow(), c.InitWindow())
		for id := int64(1); id <= 3; id++ {
			w, err := c.StreamWindow(id)
			closed, _ := c.StreamClosed(id)
			fmt.Fprintf(&sb, "stream(%d)=%d,%v,%v\n", id, w, closed, err)
		}
		return sb.String()
	}
	first := play()
	second := play()
	if first != second {
		t.Fatalf("重放结果不一致:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}

// 日志包含输入、输出与判定依据。
func TestLoggerShowsInputOutputBasis(t *testing.T) {
	log := newTestLogger(t)
	c, _ := New(10, 10, 4, WithLogger(log))
	must(t, c.OpenStream(7))
	_ = c.Send(7, 4)
	_ = c.Send(7, 4)
	_ = c.Send(7, 4) // 连接窗口仅剩 2，拒绝并说明依据

	out := log.String()
	for _, want := range []string{
		"op=open", "target=stream(7)", "=> ok",
		"op=send", "n=4 超过连接窗口 2", "=> reject: flowcontrol: insufficient connection window",
		"#1", "#4",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("日志缺少 %q:\n%s", want, out)
		}
	}
}
