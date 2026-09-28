package window

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func newTestAccumulator(t *testing.T, cfg Config, w io.Writer) *Accumulator {
	t.Helper()
	var logger *slog.Logger
	if w != nil {
		logger = slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}
	a, err := New(cfg, WithLogger(logger))
	if err != nil {
		t.Fatalf("New(%+v) unexpected error: %v", cfg, err)
	}
	return a
}

// TestNegativeTimestamp 负时间戳必须向下取整归入正确的大窗口 [-10, 0)，
// 并在子窗口终点 -5、0 上输出累计计数。
func TestNegativeTimestamp(t *testing.T) {
	var buf bytes.Buffer
	a := newTestAccumulator(t, Config{WindowSize: 10, Step: 5, MaxWindows: 10}, &buf)

	mustAdd(t, a, "n", -6)  // 窗口 [-10,0)，计入终点 -5、0
	mustAdd(t, a, "n", -5)  // 触发终点 -5：累计 1
	mustAdd(t, a, "n", -1)  // 仅计入终点 0
	mustAdd(t, a, "n", -10) // 恰好落在大窗口左边界 -10，但 minEnd=-5 <= wm=-5，迟到丢弃

	got := a.Snapshot()
	wantOutputs := []Output{
		{Key: "n", WindowStart: -10, End: -5, Cumulative: 1},
	}
	if !reflect.DeepEqual(got.Outputs, wantOutputs) {
		t.Fatalf("outputs = %+v, want %+v", got.Outputs, wantOutputs)
	}
	if got.Dropped != 1 {
		t.Fatalf("dropped = %d, want 1", got.Dropped)
	}

	// 事件 t=0 归入新窗口 [0,10)，并触发旧窗口终点 0（左闭右开，0 不属于旧窗口）。
	// 终点 0 的累计 = 事件 -6、-5、-1 三个都计入终点 0。
	r := mustAdd(t, a, "n", 0)
	if len(r.Fired) != 1 || r.Fired[0] != (Output{Key: "n", WindowStart: -10, End: 0, Cumulative: 3}) {
		t.Fatalf("fired = %+v, want single (n,-10,0,3)", r.Fired)
	}
	// 日志中必须包含输入、输出与判定依据（slog text 格式）。
	log := buf.String()
	for _, want := range []string{"window.Add accepted", "key=n", "timestamp=-6", "firedCount", "window.Add dropped"} {
		if !strings.Contains(log, want) {
			t.Errorf("log missing %q\nfull log:\n%s", want, log)
		}
	}
}

// TestBoundaryEvent 恰好落在大窗口边界的事件归入右侧新窗口（左闭右开）。
func TestBoundaryEvent(t *testing.T) {
	a := newTestAccumulator(t, Config{WindowSize: 10, Step: 5, MaxWindows: 10}, nil)

	mustAdd(t, a, "a", 9)       // 窗口 [0,10)，只计入终点 10
	r := mustAdd(t, a, "a", 10) // 边界事件，归入窗口 [10,20)；水位线到 10，旧窗口输出
	if r.Fired[0] != (Output{Key: "a", WindowStart: 0, End: 5, Cumulative: 0}) ||
		r.Fired[1] != (Output{Key: "a", WindowStart: 0, End: 10, Cumulative: 1}) {
		t.Fatalf("fired = %+v, want (a,0,5,0),(a,0,10,1)", r.Fired)
	}
	if ws := a.keys["a"][0]; ws != nil {
		t.Fatalf("completed window [0,10) should have been evicted")
	}
	if ws := a.keys["a"][10]; ws == nil {
		t.Fatalf("boundary event t=10 must belong to window [10,20)")
	}
}

// TestCumulativeOutputs 逐级扩大子窗口的累计计数序列。
func TestCumulativeOutputs(t *testing.T) {
	a := newTestAccumulator(t, Config{WindowSize: 10, Step: 5, MaxWindows: 10}, nil)

	// 窗口 [0,10)：t=0、t=4 同时计入终点 5 和 10；t=5、t=9 只计入终点 10。
	for _, ts := range []int64{0, 4, 5, 9} {
		mustAdd(t, a, "a", ts)
	}
	mustAdd(t, a, "a", 10) // 触发 [0,10) 全部剩余子窗口
	// 窗口 [10,20)：t=10、t=14 计入终点 15、20；t=15 只计入终点 20。
	mustAdd(t, a, "a", 14)
	mustAdd(t, a, "a", 15)
	mustAdd(t, a, "a", 20)

	want := []Output{
		{Key: "a", WindowStart: 0, End: 5, Cumulative: 2},
		{Key: "a", WindowStart: 0, End: 10, Cumulative: 4},
		{Key: "a", WindowStart: 10, End: 15, Cumulative: 2},
		{Key: "a", WindowStart: 10, End: 20, Cumulative: 3},
	}
	if got := a.Snapshot().Outputs; !reflect.DeepEqual(got, want) {
		t.Fatalf("outputs = %+v\nwant     %+v", got, want)
	}
}

// TestDropWhenWatermarkEqualsMinEnd 水位线恰好等于最小子窗口终点时必须丢弃，
// 且丢弃不改变已输出结果。
func TestDropWhenWatermarkEqualsMinEnd(t *testing.T) {
	a := newTestAccumulator(t, Config{WindowSize: 10, Step: 5, MaxWindows: 10}, nil)

	mustAdd(t, a, "c", 0)
	r := mustAdd(t, a, "c", 5) // wm=5，先触发终点 5
	if len(r.Fired) != 1 {
		t.Fatalf("fired = %+v, want one output", r.Fired)
	}
	before := a.Snapshot()

	// t=0 的最小子窗口终点恰为 5，5 <= 水位线 5，丢弃。
	r, err := a.Add("c", 0)
	if err != nil {
		t.Fatalf("late event must be dropped, not rejected: %v", err)
	}
	if !r.Dropped || r.MinEnd != 5 || r.WatermarkAfter != 5 {
		t.Fatalf("expected dropped with minEnd=5 wm=5, got %+v", r)
	}
	after := a.Snapshot()
	if after.Dropped != before.Dropped+1 || after.Watermark != before.Watermark ||
		!reflect.DeepEqual(after.Outputs, before.Outputs) {
		t.Fatalf("drop changed more than dropped counter:\nbefore=%+v\nafter =%+v", before, after)
	}
}

// TestZeroCountStillFired 计数为零的子窗口到期时也必须输出。
func TestZeroCountStillFired(t *testing.T) {
	a := newTestAccumulator(t, Config{WindowSize: 10, Step: 5, MaxWindows: 10}, nil)

	mustAdd(t, a, "b", 5)       // 不计入终点 5
	r := mustAdd(t, a, "b", 10) // wm=10：终点 5（计数 0）与终点 10（计数 1）都输出
	want := []Output{
		{Key: "b", WindowStart: 0, End: 5, Cumulative: 0},
		{Key: "b", WindowStart: 0, End: 10, Cumulative: 1},
	}
	if !reflect.DeepEqual(r.Fired, want) {
		t.Fatalf("fired = %+v, want %+v", r.Fired, want)
	}
}

// TestInvalidConfig 各类非法构造参数必须返回可区分的 ErrInvalidParameter。
func TestInvalidConfig(t *testing.T) {
	bad := []Config{
		{WindowSize: 0, Step: 5, MaxWindows: 1},
		{WindowSize: -10, Step: 5, MaxWindows: 1},
		{WindowSize: 10, Step: 0, MaxWindows: 1},
		{WindowSize: 10, Step: -5, MaxWindows: 1},
		{WindowSize: 10, Step: 3, MaxWindows: 1}, // 步长不整除大窗口
		{WindowSize: 10, Step: 5, MaxWindows: 0},
	}
	for i, cfg := range bad {
		if _, err := New(cfg); !errors.Is(err, ErrInvalidParameter) {
			t.Errorf("case %d cfg=%+v: err = %v, want ErrInvalidParameter", i, cfg, err)
		}
	}
}

// TestEmptyKey 空键被拒绝且不改变任何状态。
func TestEmptyKey(t *testing.T) {
	a := newTestAccumulator(t, Config{WindowSize: 10, Step: 5, MaxWindows: 10}, nil)
	mustAdd(t, a, "a", 0)
	before := a.Snapshot()

	_, err := a.Add("", 7)
	if !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("err = %v, want ErrEmptyKey", err)
	}
	after := a.Snapshot()
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("rejected empty key changed state:\nbefore=%+v\nafter =%+v", before, after)
	}
}

// TestTooManyWindows 同时保留的（键 × 大窗口）状态超限时拒绝，且状态不变。
func TestTooManyWindows(t *testing.T) {
	a := newTestAccumulator(t, Config{WindowSize: 10, Step: 5, MaxWindows: 2}, nil)

	mustAdd(t, a, "a", 9) // a/[0,10) 保留（终点 10 > wm 9）
	mustAdd(t, a, "b", 9) // b/[0,10) 保留，达上限
	before := a.Snapshot()

	_, err := a.Add("c", 9)
	if !errors.Is(err, ErrTooManyWindows) {
		t.Fatalf("err = %v, want ErrTooManyWindows", err)
	}
	after := a.Snapshot()
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("rejected add changed state:\nbefore=%+v\nafter =%+v", before, after)
	}
	if a.total != 2 {
		t.Fatalf("retained = %d, want 2", a.total)
	}

	// 一个窗口到期回收后，新窗口可以正常创建。
	mustAdd(t, a, "a", 10) // wm=10：a/[0,10) 输出完毕被回收
	mustAdd(t, a, "c", 9)  // minEnd=10 <= wm=10，迟到丢弃而非占坑
	mustAdd(t, a, "c", 10) // c/[10,20) 正常创建
}

// TestErrorSentinelsDistinct 三类拒绝原因必须可区分。
func TestErrorSentinelsDistinct(t *testing.T) {
	if errors.Is(ErrEmptyKey, ErrInvalidParameter) ||
		errors.Is(ErrTooManyWindows, ErrInvalidParameter) ||
		errors.Is(ErrTooManyWindows, ErrEmptyKey) {
		t.Fatal("error sentinels must be distinct and not wrap each other")
	}
}

// TestDeterminism 同一输入序列重复计算必须得到完全相同的输出。
func TestDeterminism(t *testing.T) {
	seq := [][2]interface{}{
		{"a", int64(3)}, {"b", int64(-7)}, {"a", int64(7)}, {"b", int64(-3)},
		{"a", int64(12)}, {"b", int64(0)}, {"a", int64(19)}, {"b", int64(5)},
	}
	run := func() Snapshot {
		a := newTestAccumulator(t, Config{WindowSize: 10, Step: 5, MaxWindows: 10}, nil)
		for _, e := range seq {
			if _, err := a.Add(e[0].(string), e[1].(int64)); err != nil {
				t.Fatalf("Add(%v,%v) error: %v", e[0], e[1], err)
			}
		}
		return a.Snapshot()
	}
	first, second := run(), run()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("non-deterministic:\nfirst = %+v\nsecond= %+v", first, second)
	}
}

// TestInitialWatermark 首个事件之前水位线为负无穷，负时间戳不会被误丢。
func TestInitialWatermark(t *testing.T) {
	a := newTestAccumulator(t, Config{WindowSize: 10, Step: 5, MaxWindows: 10}, nil)
	if got := a.Snapshot().Watermark; got != math.MinInt64 {
		t.Fatalf("initial watermark = %d, want MinInt64", got)
	}
	r := mustAdd(t, a, "z", -100)
	if r.Dropped {
		t.Fatal("first event with negative timestamp must not be dropped")
	}
	if r.WatermarkAfter != -100 {
		t.Fatalf("watermark = %d, want -100", r.WatermarkAfter)
	}
}

// TestSnapshotIsConsistentAndDefensive 并发读取快照：逐字段一致且互不影响。
func TestSnapshotIsConsistentAndDefensive(t *testing.T) {
	a := newTestAccumulator(t, Config{WindowSize: 10, Step: 5, MaxWindows: 1000}, nil)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)

	go func() { // 写者：多键、推进时间
		defer wg.Done()
		for i := int64(0); i < 300; i++ {
			key := string(rune('A'+i%20)) + "-" + string(rune('a'+i%26))
			if _, err := a.Add(key, i*3); err != nil {
				t.Errorf("Add error: %v", err)
				return
			}
		}
		close(stop)
	}()

	go func() { // 读者：快照必须自洽
		defer wg.Done()
		for {
			s := a.Snapshot()
			for i := 1; i < len(s.Outputs); i++ { // history 全局按 (End,Key) 有序
				p, c := s.Outputs[i-1], s.Outputs[i]
				if p.End > c.End || (p.End == c.End && p.Key > c.Key) {
					t.Errorf("history not sorted: %+v before %+v", p, c)
				}
			}
			if len(s.Outputs) > 0 {
				s.Outputs[0].Cumulative = -42 // 篡改快照不得影响内部状态
			}
			select {
			case <-stop:
				return
			default:
			}
		}
	}()
	wg.Wait()

	s1 := a.Snapshot()
	s1.Outputs[0] = Output{}
	s2 := a.Snapshot()
	if reflect.DeepEqual(s1.Outputs, s2.Outputs) {
		t.Fatal("mutating returned snapshot must not affect accumulator state")
	}
}

func mustAdd(t *testing.T, a *Accumulator, key string, ts int64) AddResult {
	t.Helper()
	r, err := a.Add(key, ts)
	if err != nil {
		t.Fatalf("Add(%q,%d) unexpected error: %v", key, ts, err)
	}
	return r
}
