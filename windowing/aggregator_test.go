package windowing

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"
)

// logBuffer 是线程安全的测试日志，记录输入、输出与判定依据。
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// TestNegativeEventTime 事件时间为负必须拒绝，且不改变任何状态。
func TestNegativeEventTime(t *testing.T) {
	h := newHarness(t, 4, 0)
	_, err := h.a.Submit(Event{Key: "k", EventTime: -1, Value: 1})
	if !errors.Is(err, ErrNegativeEventTime) {
		t.Fatalf("err=%v 想要 ErrNegativeEventTime", err)
	}
	if h.a.Watermark() != 0 || h.a.LateCount() != 0 || h.a.AcceptedCount() != 0 {
		t.Fatalf("非法事件改变了状态: wm=%d late=%d accepted=%d",
			h.a.Watermark(), h.a.LateCount(), h.a.AcceptedCount())
	}
	// 合法事件仍可正常提交。
	h.submit(t, Event{Key: "k", EventTime: 0, Value: 1})
	if h.a.AcceptedCount() != 1 {
		t.Fatalf("accepted=%d 想要 1", h.a.AcceptedCount())
	}
	t.Logf("判定依据日志:\n%s", h.log.String())
}

// TestRejectedResize 覆盖四类拒绝原因、校验顺序及“拒绝不改变状态”。
func TestRejectedResize(t *testing.T) {
	t.Run("non_positive", func(t *testing.T) {
		h := newHarness(t, 4, 0)
		h.submit(t, Event{Key: "k", EventTime: 1, Value: 1})
		wm, inflight, late := h.a.Watermark(), h.a.InFlightCount(), h.a.LateCount()
		for _, bad := range []int64{0, -1, -100} {
			_, err := h.a.RequestResize(bad)
			if rejectReason(err) != ResizeNonPositive {
				t.Fatalf("newSize=%d reason=%v 想要 ResizeNonPositive", bad, err)
			}
		}
		snapshotUnchanged(t, h, wm, inflight, late, 0)
	})

	t.Run("pending", func(t *testing.T) {
		h := newHarness(t, 4, 0)
		h.submit(t, Event{Key: "k", EventTime: 5, Value: 1})
		wm, inflight, late := h.a.Watermark(), h.a.InFlightCount(), h.a.LateCount()
		b, err := h.a.RequestResize(6) // M=8, B=12，保持待完成
		if err != nil || b != 12 {
			t.Fatalf("boundary=%d err=%v", b, err)
		}
		_, err = h.a.RequestResize(8)
		if rejectReason(err) != ResizePending {
			t.Fatalf("reason=%v 想要 ResizePending", err)
		}
		snapshotUnchanged(t, h, wm, inflight, late, 0)
	})

	t.Run("same_size", func(t *testing.T) {
		h := newHarness(t, 4, 0)
		_, err := h.a.RequestResize(4)
		if rejectReason(err) != ResizeSameSize {
			t.Fatalf("reason=%v 想要 ResizeSameSize", err)
		}
		if h.a.PendingResize() {
			t.Fatalf("相同大小的申请不应登记为待完成升级")
		}
	})

	t.Run("validation_order_non_positive_before_pending", func(t *testing.T) {
		h := newHarness(t, 4, 0)
		h.submit(t, Event{Key: "k", EventTime: 5, Value: 1})
		if _, err := h.a.RequestResize(6); err != nil {
			t.Fatal(err)
		}
		// 有待完成升级时申请非正大小：按序先报“非正”。
		if _, err := h.a.RequestResize(0); rejectReason(err) != ResizeNonPositive {
			t.Fatalf("reason=%v 想要 ResizeNonPositive（顺序应先于 pending）", err)
		}
		// 有待完成升级时申请与当前相同的大小：pending 先于 same-size 报告。
		if _, err := h.a.RequestResize(4); rejectReason(err) != ResizePending {
			t.Fatalf("reason=%v 想要 ResizePending（顺序应先于 same-size）", err)
		}
	})

	t.Run("lcm_too_large", func(t *testing.T) {
		// gcd(1_000_000, 999_999)=1，lcm=999_999_000_000 > 10^9。
		h := newHarness(t, 1_000_000, 0)
		_, err := h.a.RequestResize(999_999)
		if rejectReason(err) != ResizeLCMTooLarge {
			t.Fatalf("reason=%v 想要 ResizeLCMTooLarge", err)
		}
		if h.a.PendingResize() || h.a.CurrentSize() != 1_000_000 {
			t.Fatalf("拒绝后状态改变: pending=%v size=%d", h.a.PendingResize(), h.a.CurrentSize())
		}
	})

	t.Run("lcm_equal_limit_allowed", func(t *testing.T) {
		// lcm(10^9, 1)=10^9，不“超过”10^9，必须允许。
		h := newHarness(t, 1_000_000_000, 0)
		b, err := h.a.RequestResize(1)
		if err != nil {
			t.Fatalf("lcm 恰为 10^9 应允许: %v", err)
		}
		if b != 0 { // 无任何状态、WM=0：M=0，B=0
			t.Fatalf("boundary=%d 想要 0", b)
		}
		if h.a.CurrentSize() != 1 {
			t.Fatalf("size=%d 想要 1", h.a.CurrentSize())
		}
	})

	t.Run("rejection_keeps_state", func(t *testing.T) {
		h := newHarness(t, 4, 0)
		h.submit(t, Event{Key: "k", EventTime: 3, Value: 7}) // WM=3，[0,4) 在途
		h.submit(t, Event{Key: "k", EventTime: 0, Value: 2}) // WM 不变，[0,4) 累计 2 条
		wm, inflight, late := h.a.Watermark(), h.a.InFlightCount(), h.a.LateCount()
		outBefore := len(h.outputs())
		for _, apply := range []func() error{
			func() error { _, e := h.a.RequestResize(0); return e },
			func() error { _, e := h.a.RequestResize(-3); return e },
			func() error { _, e := h.a.RequestResize(4); return e },
			func() error { _, e := h.a.RequestResize(999_999_937); return e }, // 大质数，lcm(4,?) 远超 1e9
		} {
			if err := apply(); err == nil {
				t.Fatalf("期望申请被拒绝")
			}
		}
		snapshotUnchanged(t, h, wm, inflight, late, outBefore)
	})
}

func snapshotUnchanged(t *testing.T, h *testHarness, wm, inflight, late int64, outLen int) {
	t.Helper()
	if h.a.Watermark() != wm || h.a.InFlightCount() != inflight ||
		h.a.LateCount() != late || len(h.outputs()) != outLen {
		t.Fatalf("拒绝操作改变了状态: wm %d->%d inflight %d->%d late %d->%d outputs %d->%d",
			wm, h.a.Watermark(), inflight, h.a.InFlightCount(), late, h.a.LateCount(),
			outLen, len(h.outputs()))
	}
}

// TestWatermarkMonotonicWithDelay 水位取 max(自身, maxSeen-delay)，只增不减。
func TestWatermarkMonotonicWithDelay(t *testing.T) {
	h := newHarness(t, 4, 2)
	h.submit(t, Event{Key: "k", EventTime: 10, Value: 1}) // WM=max(0,8)=8
	if h.a.Watermark() != 8 {
		t.Fatalf("wm=%d 想要 8", h.a.Watermark())
	}
	h.submit(t, Event{Key: "k", EventTime: 5, Value: 1}) // 候选 3，WM 保持 8；右端 8<=8 迟到
	if h.a.Watermark() != 8 || h.a.LateCount() != 1 {
		t.Fatalf("wm=%d late=%d，想要 wm=8 late=1", h.a.Watermark(), h.a.LateCount())
	}
	checkInvariant(t, h)
	t.Logf("判定依据日志:\n%s", h.log.String())
}

// TestLCMOverflowSafe 输入极大时 lcm 检测必须安全返回“超限”而非溢出。
func TestLCMOverflowSafe(t *testing.T) {
	// lcm(maxint64, 2) 远超 int64：乘法溢出被检测，升级按“超过 10^9”拒绝。
	h := newHarness(t, 1<<62, 0)
	_, err := h.a.RequestResize(3)
	if rejectReason(err) != ResizeLCMTooLarge {
		t.Fatalf("reason=%v 想要 ResizeLCMTooLarge（且不得 panic/溢出）", err)
	}
	// 可整除时 lcm 不放大：size=2^62，新大小 2，lcm=2^62 仍 >1e9，拒绝。
	_, err = h.a.RequestResize(2)
	if rejectReason(err) != ResizeLCMTooLarge {
		t.Fatalf("reason=%v 想要 ResizeLCMTooLarge", err)
	}
}

// TestNewInvalidConfig 非法构造参数必须立即报错（panic），而非产出畸形聚合器。
func TestNewInvalidConfig(t *testing.T) {
	for _, cfg := range []Config{
		{InitialSize: 0, AllowedLateness: 0},
		{InitialSize: -1, AllowedLateness: 0},
		{InitialSize: 4, AllowedLateness: -1},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("非法配置应 panic: %+v", cfg)
				}
			}()
			New(cfg)
		}()
	}
}

// TestConcurrentSubmitAndResize 并发提交事件与升级申请：恒等不变量必须成立，无数据竞争。
func TestConcurrentSubmitAndResize(t *testing.T) {
	h := newHarness(t, 4, 0)
	const n = 500
	var wg sync.WaitGroup

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := h.a.Submit(Event{
				Key:       fmt.Sprintf("k%d", i%7),
				EventTime: int64(i),
				Value:     int64(i),
			})
			if err != nil {
				t.Errorf("并发提交出错: %v", err)
			}
		}(i)
	}

	// 多个 goroutine 并发升级：成功的申请其生效点必须是 lcm 的倍数；
	// 拒绝只可能是四类已定义原因之一。
	sizes := []int64{6, 8, 3, 6, 10}
	for _, s := range sizes {
		wg.Add(1)
		go func(s int64) {
			defer wg.Done()
			b, err := h.a.RequestResize(s)
			if err != nil {
				if rejectReason(err) == 0 {
					t.Errorf("升级返回未知错误: %v", err)
				}
				return
			}
			if b < 0 || b%12 != 0 && s == 6 { // 4 与 6 的 lcm=12
				t.Errorf("非法生效点 b=%d", b)
			}
		}(s)
	}

	wg.Wait()

	// 总提交数 = 已输出条数 + 迟到数 + 在途窗口内条数。
	total := h.a.EmittedCount() + h.a.LateCount() + h.a.InFlightCount()
	if total != n {
		t.Fatalf("总提交数恒等式: emitted=%d + late=%d + inflight=%d = %d != 提交数 %d",
			h.a.EmittedCount(), h.a.LateCount(), h.a.InFlightCount(), total, n)
	}
	// 全部事件已按某一串行顺序被恰好处理一次：accepted+late 即总提交数。
	if h.a.AcceptedCount()+h.a.LateCount() != n {
		t.Fatalf("accepted=%d + late=%d != %d", h.a.AcceptedCount(), h.a.LateCount(), n)
	}
	// 输出必须按（右端、键）升序。
	got := h.outputs()
	for i := 1; i < len(got); i++ {
		if got[i-1].End > got[i].End ||
			(got[i-1].End == got[i].End && got[i-1].Key > got[i].Key) {
			t.Fatalf("输出未按（右端,键）升序: %+v 在 %+v 之后", got[i], got[i-1])
		}
	}
}

// op 是可重放的确定性操作脚本。
type op struct {
	event  *Event
	resize int64
}

func runScript(t *testing.T, ops []op) ([]Result, int64, int64) {
	t.Helper()
	h := newHarness(t, 4, 0)
	for _, o := range ops {
		if o.event != nil {
			h.a.Submit(*o.event)
		} else {
			h.a.RequestResize(o.resize)
		}
	}
	return h.outputs(), h.a.LateCount(), h.a.Watermark()
}

// TestReplayDeterminism 同一操作序列重放必须得到完全相同的输出。
func TestReplayDeterminism(t *testing.T) {
	script := []op{
		{event: &Event{Key: "a", EventTime: 3, Value: 1}},
		{event: &Event{Key: "b", EventTime: 11, Value: 2}},
		{resize: 6},
		{resize: 8}, // 被拒：已有待完成升级
		{event: &Event{Key: "a", EventTime: 7, Value: 10}}, // 旧划分 [4,8) 右端8<=WM11 迟到
		{event: &Event{Key: "a", EventTime: 10, Value: 3}},
		{event: &Event{Key: "b", EventTime: 13, Value: 4}},
		{resize: 0}, // 被拒：非正
		{event: &Event{Key: "a", EventTime: 20, Value: 5}},
		{event: &Event{Key: "c", EventTime: 13, Value: 6}},
		{resize: 8}, // 升级完成后可能成功（6->8）
		{event: &Event{Key: "a", EventTime: 30, Value: 7}},
		{event: &Event{Key: "b", EventTime: 2, Value: 9}}, // 大概率迟到
	}
	out1, late1, wm1 := runScript(t, script)
	out2, late2, wm2 := runScript(t, script)
	if late1 != late2 || wm1 != wm2 || len(out1) != len(out2) {
		t.Fatalf("重放不一致: late %d/%d wm %d/%d len %d/%d",
			late1, late2, wm1, wm2, len(out1), len(out2))
	}
	for i := range out1 {
		if out1[i] != out2[i] {
			t.Fatalf("重放输出[%d] 不一致: %+v vs %+v", i, out1[i], out2[i])
		}
	}
	if len(out1) == 0 || late1 == 0 {
		t.Fatalf("脚本应同时产生输出与迟到事件，out=%d late=%d", len(out1), late1)
	}
}

// TestPendingEventsOnBothSidesOfB 升级期间事件时间在 B 两侧的归属。
func TestPendingEventsOnBothSidesOfB(t *testing.T) {
	// 4->6，delay=0：(k,3) 后 WM=3、持有 [0,4)；申请时 M=4，B=12。
	h := newHarness(t, 4, 0)
	h.submit(t, Event{Key: "k", EventTime: 3, Value: 1})
	b, err := h.a.RequestResize(6)
	if err != nil || b != 12 {
		t.Fatalf("boundary=%d err=%v, want 12", b, err)
	}

	// B 左侧：t=10<12 按旧大小 4 归 [8,12)。
	h.submit(t, Event{Key: "k", EventTime: 10, Value: 5})
	// B 右侧：t=12>=12 按新大小 6 归 [12,18)；提交后 WM=12>=B 完成升级。
	h.submit(t, Event{Key: "k", EventTime: 12, Value: 50})

	want := []Result{
		{Key: "k", Start: 0, End: 4, Count: 1, Sum: 1},
		{Key: "k", Start: 8, End: 12, Count: 1, Sum: 5},
	}
	got := h.outputs()
	if len(got) != len(want) {
		t.Fatalf("输出数量=%d 想要 %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("输出[%d]=%+v 想要 %+v", i, got[i], want[i])
		}
	}
	if h.a.CurrentSize() != 6 {
		t.Fatalf("当前大小=%d 想要 6", h.a.CurrentSize())
	}
	// 仅新划分 [12,18) 在途，恰好包含一条 B 右侧事件。
	if h.a.InFlightCount() != 1 {
		t.Fatalf("在途条数=%d 想要 1", h.a.InFlightCount())
	}
	checkInvariant(t, h)
	t.Logf("判定依据日志:\n%s", h.log.String())
}

// TestOldWindowEmittedBeforeNew 升级后旧窗口先输出；同右端按键升序。
func TestOldWindowEmittedBeforeNew(t *testing.T) {
	h := newHarness(t, 4, 0)
	h.submit(t, Event{Key: "a", EventTime: 11, Value: 1})
	h.submit(t, Event{Key: "b", EventTime: 11, Value: 2})
	if b, err := h.a.RequestResize(6); err != nil || b != 12 { // M=12, B=12
		t.Fatalf("boundary=%d err=%v", b, err)
	}
	h.submit(t, Event{Key: "a", EventTime: 13, Value: 10}) // 新划分 [12,18)
	h.submit(t, Event{Key: "b", EventTime: 13, Value: 20})
	h.submit(t, Event{Key: "c", EventTime: 18, Value: 0}) // 推进 WM=18 并刷出到期窗口

	want := []Result{
		{Key: "a", Start: 8, End: 12, Count: 1, Sum: 1},
		{Key: "b", Start: 8, End: 12, Count: 1, Sum: 2},
		{Key: "a", Start: 12, End: 18, Count: 1, Sum: 10},
		{Key: "b", Start: 12, End: 18, Count: 1, Sum: 20},
	}
	got := h.outputs()
	if len(got) != len(want) {
		t.Fatalf("输出=%+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("输出[%d]=%+v 想要 %+v", i, got[i], want[i])
		}
	}
	checkInvariant(t, h)
	t.Logf("判定依据日志:\n%s", h.log.String())
}

// TestLatenessUnderBothPartitions 迟到判定分别按旧划分与新划分。
func TestLatenessUnderBothPartitions(t *testing.T) {
	t.Run("old_partition_before_resize", func(t *testing.T) {
		// delay=0：(k,5) 后 WM=5；t=3 在旧大小 4 下属 [0,4)，右端 4<=5 => 迟到。
		h := newHarness(t, 4, 0)
		h.submit(t, Event{Key: "k", EventTime: 5, Value: 1})
		if late := h.submit(t, Event{Key: "k", EventTime: 3, Value: 9}); !late {
			t.Fatalf("t=3 应按旧划分判迟到")
		}
		if h.a.LateCount() != 1 {
			t.Fatalf("lateCount=%d 想要 1", h.a.LateCount())
		}
		checkInvariant(t, h)
		t.Logf("判定依据日志:\n%s", h.log.String())
	})

	t.Run("old_partition_during_pending", func(t *testing.T) {
		// (k,8) 后 WM=8、持有 [8,12)；申请 4->6 得 B=12。
		// t=7 在旧划分下属 [4,8)，右端 8<=WM=8 判迟到；
		// 若错按新大小 6，它会落入 [6,12)（右端 12>8）而被计入。
		h := newHarness(t, 4, 0)
		h.submit(t, Event{Key: "k", EventTime: 8, Value: 1})
		if b, err := h.a.RequestResize(6); err != nil || b != 12 {
			t.Fatalf("boundary=%d err=%v", b, err)
		}
		if late := h.submit(t, Event{Key: "k", EventTime: 7, Value: 9}); !late {
			t.Fatalf("t=7 应按旧划分判迟到")
		}
		// 迟到值 9 不得泄漏进任何窗口：推进到升级完成后核对旧桶 [8,12) 的 sum。
		h.submit(t, Event{Key: "k", EventTime: 12, Value: 0})
		for _, r := range h.outputs() {
			if r.Start == 8 && r.End == 12 && (r.Sum != 1 || r.Count != 1) {
				t.Fatalf("迟到事件污染了旧窗口: %+v", r)
			}
		}
		if h.a.LateCount() != 1 {
			t.Fatalf("lateCount=%d 想要 1", h.a.LateCount())
		}
		checkInvariant(t, h)
		t.Logf("判定依据日志:\n%s", h.log.String())
	})

	t.Run("new_partition_during_pending", func(t *testing.T) {
		// (k,10) 使 WM=10、持有 [8,12)，申请 4->6 得 B=12。
		// t=6 在 B 左侧：旧大小 4 下属 [4,8)，右端 8<=10 判迟到；
		// 若错按 B 右侧的新大小 6，它会落入 [6,12)（右端 12>10）而被计入。
		h := newHarness(t, 4, 0)
		h.submit(t, Event{Key: "k", EventTime: 10, Value: 1}) // WM=10，[8,12) 持有
		if b, err := h.a.RequestResize(6); err != nil || b != 12 {
			t.Fatalf("boundary=%d err=%v", b, err)
		}
		if late := h.submit(t, Event{Key: "k", EventTime: 6, Value: 9}); !late {
			t.Fatalf("t=6 在 B=12 左侧必须按旧划分判迟到（按新划分本可计入 [6,12)）")
		}
		h.submit(t, Event{Key: "k", EventTime: 12, Value: 0})
		if h.a.LateCount() != 1 {
			t.Fatalf("lateCount=%d 想要 1（t=6 按旧划分迟到）", h.a.LateCount())
		}
		checkInvariant(t, h)
		t.Logf("判定依据日志:\n%s", h.log.String())
	})

	t.Run("new_partition_after_resize", func(t *testing.T) {
		// 升级完成后，迟到判定按新大小：delay=0，4->6 已在 B=12 完成。
		// (k,18) 后 WM=18；t=13 在新大小 6 下属 [12,18)，右端 18<=18 => 迟到。
		h := newHarness(t, 4, 0)
		h.submit(t, Event{Key: "k", EventTime: 11, Value: 1})
		if b, err := h.a.RequestResize(6); err != nil || b != 12 {
			t.Fatalf("boundary=%d err=%v", b, err)
		}
		h.submit(t, Event{Key: "k", EventTime: 18, Value: 2}) // 完成升级，WM=18
		if h.a.CurrentSize() != 6 {
			t.Fatalf("升级应已完成，size=%d", h.a.CurrentSize())
		}
		if late := h.submit(t, Event{Key: "k", EventTime: 13, Value: 9}); !late {
			t.Fatalf("t=13 应在新大小 6 下按 [12,18) 判迟到")
		}
		if h.a.LateCount() != 1 {
			t.Fatalf("lateCount=%d 想要 1", h.a.LateCount())
		}
		checkInvariant(t, h)
		t.Logf("判定依据日志:\n%s", h.log.String())
	})
}

func (l *logBuffer) Printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(&l.buf, format+"\n", args...)
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

type testHarness struct {
	a   *Aggregator
	out []Result
	mu  sync.Mutex
	log *logBuffer
}

func newHarness(t *testing.T, initialSize, delay int64) *testHarness {
	t.Helper()
	h := &testHarness{log: &logBuffer{}}
	h.a = New(Config{
		InitialSize:     initialSize,
		AllowedLateness: delay,
		Emit: func(r Result) {
			h.mu.Lock()
			h.out = append(h.out, r)
			h.mu.Unlock()
		},
		Logger: h.log,
	})
	return h
}

func (h *testHarness) outputs() []Result {
	h.mu.Lock()
	defer h.mu.Unlock()
	cp := make([]Result, len(h.out))
	copy(cp, h.out)
	return cp
}

func (h *testHarness) submit(t *testing.T, e Event) bool {
	t.Helper()
	late, err := h.a.Submit(e)
	if err != nil {
		t.Fatalf("submit %+v: %v", e, err)
	}
	return late
}

func checkInvariant(t *testing.T, h *testHarness) {
	t.Helper()
	accepted := h.a.AcceptedCount()
	late := h.a.LateCount()
	emitted := h.a.EmittedCount()
	inflight := h.a.InFlightCount()
	if accepted != emitted+inflight {
		t.Fatalf("invariant accepted=%d != emitted(%d)+inflight(%d), late=%d",
			accepted, emitted, inflight, late)
	}
	var outCount int64
	for _, r := range h.outputs() {
		outCount += r.Count
	}
	if outCount != emitted {
		t.Fatalf("输出条数=%d != EmittedCount=%d", outCount, emitted)
	}
}

func rejectReason(err error) ResizeRejectReason {
	var re *ResizeError
	if errors.As(err, &re) {
		return re.Reason
	}
	return 0
}

// TestResizeBoundary4to6 覆盖 4->6 时 M 恰为 12（B=12）与略超 12（B=24）。
func TestResizeBoundary4to6(t *testing.T) {
	t.Run("M_exactly_12_gives_B_12", func(t *testing.T) {
		// delay=0：(k,11) 后 WM=11，持有窗口右端 12，M=max(11,12)=12，B=12。
		h := newHarness(t, 4, 0)
		h.submit(t, Event{Key: "k", EventTime: 11, Value: 1})

		b, err := h.a.RequestResize(6)
		if err != nil {
			t.Fatalf("resize: %v", err)
		}
		if b != 12 {
			t.Fatalf("boundary: got %d want 12", b)
		}
		if !h.a.PendingResize() || h.a.CurrentSize() != 4 {
			t.Fatalf("应处于待完成升级且当前大小仍为 4，pending=%v size=%d",
				h.a.PendingResize(), h.a.CurrentSize())
		}

		// t=12 >= B：按新大小 6 归 [12,18)。
		h.submit(t, Event{Key: "k", EventTime: 12, Value: 10})
		// t=18：WM=18>=12 完成升级；旧窗口 [8,12) 先于新窗口 [12,18) 输出。
		h.submit(t, Event{Key: "k", EventTime: 18, Value: 100})

		if h.a.CurrentSize() != 6 || h.a.PendingResize() {
			t.Fatalf("升级未完成: size=%d pending=%v", h.a.CurrentSize(), h.a.PendingResize())
		}
		got := h.outputs()
		want := []Result{
			{Key: "k", Start: 8, End: 12, Count: 1, Sum: 1},
			{Key: "k", Start: 12, End: 18, Count: 1, Sum: 10},
		}
		if len(got) != len(want) {
			t.Fatalf("输出数量=%d 想要 %d: %+v", len(got), len(want), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("输出[%d]=%+v 想要 %+v", i, got[i], want[i])
			}
		}
		checkInvariant(t, h)
		t.Logf("判定依据日志:\n%s", h.log.String())
	})

	t.Run("M_just_over_12_gives_B_24", func(t *testing.T) {
		// (k,12)：WM=12，持有 [12,16)，M=16；16 不是 lcm=12 的倍数，B=24。
		h := newHarness(t, 4, 0)
		h.submit(t, Event{Key: "k", EventTime: 12, Value: 7})

		b, err := h.a.RequestResize(6)
		if err != nil {
			t.Fatalf("resize: %v", err)
		}
		if b != 24 {
			t.Fatalf("boundary: got %d want 24", b)
		}

		// t=20 < B：仍按旧大小 4 归 [20,24)；WM=20 不输出它。
		h.submit(t, Event{Key: "k", EventTime: 20, Value: 2})
		for _, r := range h.outputs() {
			if r.End > 20 {
				t.Fatalf("WM=20 不应输出右端 %d 的窗口", r.End)
			}
		}
		// t=24 >= B：按新大小 6 归 [24,30)；WM=24>=B 完成升级并输出旧侧 [20,24)。
		h.submit(t, Event{Key: "k", EventTime: 24, Value: 3})

		if h.a.CurrentSize() != 6 {
			t.Fatalf("完成后当前大小=%d 想要 6", h.a.CurrentSize())
		}
		var oldWin Result
		var found bool
		for _, r := range h.outputs() {
			if r.Start == 20 && r.End == 24 {
				oldWin, found = r, true
			}
		}
		if !found || oldWin.Count != 1 || oldWin.Sum != 2 {
			t.Fatalf("旧侧窗口 [20,24) 未正确输出: %+v", h.outputs())
		}
		if h.a.InFlightCount() != 1 {
			t.Fatalf("在途条数=%d 想要 1（[24,30)）", h.a.InFlightCount())
		}
		checkInvariant(t, h)
		t.Logf("判定依据日志:\n%s", h.log.String())
	})
}
