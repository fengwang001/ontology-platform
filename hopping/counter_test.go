package hopping

import (
	"bytes"
	"errors"
	"log"
	"math"
	"reflect"
	"strings"
	"testing"
)

func newTestCounter(t *testing.T) *Counter {
	t.Helper()
	// size=10, step=5：窗口 k 为 [5k, 5k+10)，最多重叠 2 个窗口。
	c, err := New(Config{WindowSize: 10, SlideStep: 5, MaxOpenWindows: 100})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestWindowMembership_NegativeTimestamps(t *testing.T) {
	// 逐事件推进并清空，单独验证每个负时间戳的窗口归属。
	cases := []struct {
		ts     int64
		starts []int64
	}{
		{-15, []int64{-20, -15}},
		{-10, []int64{-15, -10}}, // 恰好落在窗口起点 -10
		{-6, []int64{-15, -10}},
		{-5, []int64{-10, -5}}, // 恰好落在窗口起点 -5
		{-1, []int64{-10, -5}},
		{0, []int64{-5, 0}}, // 原点同时是窗口起点
		{1, []int64{-5, 0}},
		{4, []int64{-5, 0}},
		{5, []int64{0, 5}}, // 恰好落在窗口起点 5；右开使它不属于 [-5,5)
		{9, []int64{0, 5}},
	}
	for _, tc := range cases {
		c, err := New(Config{WindowSize: 10, SlideStep: 5, MaxOpenWindows: 100})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if err := c.Add(tc.ts, "k", 1); err != nil {
			t.Fatalf("Add(%d): %v", tc.ts, err)
		}
		got, err := c.Advance(math.MaxInt64)
		if err != nil {
			t.Fatalf("Advance: %v", err)
		}
		var starts []int64
		for _, r := range got {
			if r.Key != "k" || r.Count != 1 {
				t.Fatalf("ts=%d unexpected record %+v", tc.ts, r)
			}
			starts = append(starts, r.WindowStart)
		}
		if !reflect.DeepEqual(starts, tc.starts) {
			t.Errorf("ts=%d windows=%v, want %v", tc.ts, starts, tc.starts)
		}
	}
}

func TestEventOnWindowStart(t *testing.T) {
	c := newTestCounter(t)
	// t=5 恰好是窗口 [5,15) 的起点（包含），也是 [-5,5) 的终点（排除）。
	if err := c.Add(5, "a", 1); err != nil {
		t.Fatalf("Add: %v", err)
	}
	got, err := c.Advance(5) // 只关闭终点 <=5 的窗口
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("event at t=5 must not belong to [-5,5), got %+v", got)
	}
	got, err = c.Advance(10)
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	// 此时 [0,10) 关闭且含该事件；[5,15) 仍打开。
	want := []WindowCount{{WindowStart: 0, WindowEnd: 10, Key: "a", Count: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%+v want=%+v", got, want)
	}
}

func TestPartialLateAndFullDiscard(t *testing.T) {
	c := newTestCounter(t)
	if err := c.Add(0, "a", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Advance(10); err != nil {
		t.Fatal(err)
	}

	// t=1：归属 [-5,5) 与 [0,10)，二者终点都 <= 水位 10 -> 整条丢弃。
	if err := c.Add(1, "a", 1); err != nil {
		t.Fatalf("late event should be dropped, not rejected: %v", err)
	}
	if st := c.Snapshot(); st.DroppedEvents != 1 {
		t.Fatalf("DroppedEvents=%d, want 1", st.DroppedEvents)
	}

	// t=6：归属 [0,10)（已关闭）与 [5,15)（仍打开）-> 部分迟到，只计后者。
	if err := c.Add(6, "a", 1); err != nil {
		t.Fatal(err)
	}
	got, err := c.Advance(15)
	if err != nil {
		t.Fatal(err)
	}
	want := []WindowCount{{WindowStart: 5, WindowEnd: 15, Key: "a", Count: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("partial late: got=%+v want=%+v", got, want)
	}
	st := c.Snapshot()
	if st.DroppedEvents != 1 {
		t.Fatalf("DroppedEvents=%d, want 1", st.DroppedEvents)
	}
	if st.OpenWindows != 0 {
		t.Fatalf("OpenWindows=%d, want 0", st.OpenWindows)
	}
}

func TestAdvanceClosesOrderedByEndAndKey(t *testing.T) {
	c := newTestCounter(t)
	mustAdd := func(ts int64, key string, n int64) {
		t.Helper()
		if err := c.Add(ts, key, n); err != nil {
			t.Fatalf("Add(%d,%s): %v", ts, key, err)
		}
	}
	mustAdd(-5, "b", 1) // 计入 [-10,0), [-5,5)
	mustAdd(0, "a", 1)  // 计入 [-5,5), [0,10)
	mustAdd(0, "b", 1)  // 计入 [-5,5), [0,10)

	got, err := c.Advance(10)
	if err != nil {
		t.Fatal(err)
	}
	want := []WindowCount{
		{WindowStart: -10, WindowEnd: 0, Key: "b", Count: 1},
		{WindowStart: -5, WindowEnd: 5, Key: "a", Count: 1},
		{WindowStart: -5, WindowEnd: 5, Key: "b", Count: 2},
		{WindowStart: 0, WindowEnd: 10, Key: "a", Count: 1},
		{WindowStart: 0, WindowEnd: 10, Key: "b", Count: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%+v want=%+v", got, want)
	}

	// 每个窗口只输出一次：再次推进（水位不变）返回空且不报错。
	got2, err := c.Advance(10)
	if err != nil {
		t.Fatalf("re-advance to same watermark: %v", err)
	}
	if len(got2) != 0 {
		t.Fatalf("windows emitted twice: %+v", got2)
	}
	if st := c.Snapshot(); st.EmittedWindows != 3 || st.EmittedRecords != 5 {
		t.Fatalf("stats=%+v, want EmittedWindows=3 EmittedRecords=5", st)
	}
}

func TestClockOnlyMovesForward(t *testing.T) {
	c := newTestCounter(t)
	if _, err := c.Advance(10); err != nil {
		t.Fatal(err)
	}
	before := c.Snapshot()
	_, err := c.Advance(9)
	if !errors.Is(err, ErrClockRewind) {
		t.Fatalf("err=%v, want ErrClockRewind", err)
	}
	after := c.Snapshot()
	if after != before {
		t.Fatalf("rejected rewind changed state: before=%+v after=%+v", before, after)
	}
	// 推进到相同水位不是回退。
	if _, err := c.Advance(10); err != nil {
		t.Fatalf("advance to same watermark must succeed: %v", err)
	}
}

func TestInvalidConfig(t *testing.T) {
	cases := []Config{
		{WindowSize: 0, SlideStep: 1, MaxOpenWindows: 1},
		{WindowSize: -10, SlideStep: 1, MaxOpenWindows: 1},
		{WindowSize: 10, SlideStep: 0, MaxOpenWindows: 1},
		{WindowSize: 10, SlideStep: -1, MaxOpenWindows: 1},
		{WindowSize: 10, SlideStep: 11, MaxOpenWindows: 1}, // 步长超过窗长
		{WindowSize: 10, SlideStep: 5, MaxOpenWindows: 0},
	}
	for i, cfg := range cases {
		if _, err := New(cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("case %d: err=%v, want ErrInvalidConfig", i, err)
		}
	}
}

func TestRejectedAddLeavesStateUntouched(t *testing.T) {
	c := newTestCounter(t)
	if err := c.Add(0, "a", 2); err != nil {
		t.Fatal(err)
	}
	before := c.Snapshot()

	// 空键
	if err := c.Add(1, "", 1); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("err=%v, want ErrEmptyKey", err)
	}
	// 非正计数
	if err := c.Add(1, "b", 0); !errors.Is(err, ErrInvalidCount) {
		t.Fatalf("err=%v, want ErrInvalidCount", err)
	}
	if err := c.Add(1, "b", -3); !errors.Is(err, ErrInvalidCount) {
		t.Fatalf("err=%v, want ErrInvalidCount", err)
	}
	// 时间戳导致窗口端点溢出 int64
	if err := c.Add(math.MaxInt64, "b", 1); !errors.Is(err, ErrInvalidTimestamp) {
		t.Fatalf("err=%v, want ErrInvalidTimestamp", err)
	}
	if err := c.Add(math.MinInt64, "b", 1); !errors.Is(err, ErrInvalidTimestamp) {
		t.Fatalf("err=%v, want ErrInvalidTimestamp", err)
	}

	after := c.Snapshot()
	if after != before {
		t.Fatalf("rejected Add changed state: before=%+v after=%+v", before, after)
	}

	// 被拒绝的事件确实没有计入：关闭 [-5,5)、[0,10) 后只剩原始 2。
	got, err := c.Advance(10)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range got {
		if r.Key != "a" || r.Count != 2 {
			t.Fatalf("unexpected record after rejected adds: %+v", r)
		}
	}
}

func TestTimestampRangeBoundaries(t *testing.T) {
	// size=10：允许区间为 [MinInt64+9, MaxInt64-10]
	// （最早窗口起点 t-9 不溢出下界；最晚窗口终点 t+10 不溢出上界）。
	const size int64 = 10
	mk := func() *Counter {
		c, err := New(Config{WindowSize: size, SlideStep: 5, MaxOpenWindows: 100})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}

	if err := mk().Add(math.MaxInt64-size, "k", 1); err != nil {
		t.Errorf("MaxInt64-size should be accepted: %v", err)
	}
	if err := mk().Add(math.MaxInt64-size+1, "k", 1); !errors.Is(err, ErrInvalidTimestamp) {
		t.Errorf("MaxInt64-size+1: err=%v, want ErrInvalidTimestamp", err)
	}
	if err := mk().Add(math.MinInt64+size-1, "k", 1); err != nil {
		t.Errorf("MinInt64+size-1 should be accepted: %v", err)
	}
	if err := mk().Add(math.MinInt64+size-2, "k", 1); !errors.Is(err, ErrInvalidTimestamp) {
		t.Errorf("MinInt64+size-2: err=%v, want ErrInvalidTimestamp", err)
	}
}

func TestTumblingWindowsNoOverlap(t *testing.T) {
	// step == size：窗口互不重叠，每个事件恰好属于一个窗口（含负时间戳）。
	c, err := New(Config{WindowSize: 7, SlideStep: 7, MaxOpenWindows: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, ts := range []int64{-8, -7, -1, 0, 6, 7} {
		if err := c.Add(ts, "x", 1); err != nil {
			t.Fatalf("Add(%d): %v", ts, err)
		}
	}
	got, err := c.Advance(math.MaxInt64)
	if err != nil {
		t.Fatal(err)
	}
	want := []WindowCount{
		{WindowStart: -14, WindowEnd: -7, Key: "x", Count: 1},
		{WindowStart: -7, WindowEnd: 0, Key: "x", Count: 2},
		{WindowStart: 0, WindowEnd: 7, Key: "x", Count: 2},
		{WindowStart: 7, WindowEnd: 14, Key: "x", Count: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%+v want=%+v", got, want)
	}
}

func TestTooManyOpenWindows(t *testing.T) {
	// size=10, step=5：每个事件最多需要 2 个窗口；上限 2。
	c, err := New(Config{WindowSize: 10, SlideStep: 5, MaxOpenWindows: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Add(0, "a", 1); err != nil {
		t.Fatalf("first add fits exactly in capacity: %v", err)
	}
	before := c.Snapshot()
	// t=100 需要两个全新窗口，总数将达到 4 -> 拒绝。
	err = c.Add(100, "a", 1)
	if !errors.Is(err, ErrTooManyOpenWindows) {
		t.Fatalf("err=%v, want ErrTooManyOpenWindows", err)
	}
	after := c.Snapshot()
	if after != before {
		t.Fatalf("rejected add changed state: before=%+v after=%+v", before, after)
	}
	// 关闭旧窗口腾出容量后，同一事件可以被接受。
	if _, err := c.Advance(100); err != nil {
		t.Fatal(err)
	}
	if err := c.Add(100, "a", 1); err != nil {
		t.Fatalf("add after closing windows: %v", err)
	}
}

func TestDeterministicReplay(t *testing.T) {
	// 同一输入序列用两个计数器各跑一遍（含乱序、迟到、多次推进），结果必须逐字节一致。
	seq := []struct {
		op        string
		ts        int64
		key       string
		count     int64
		watermark int64
	}{
		{"add", -7, "c", 1, 0},
		{"add", -5, "a", 2, 0},
		{"add", -3, "b", 1, 0},
		{"advance", 0, "", 0, 0},
		{"add", 2, "a", 1, 0},
		{"add", 2, "b", 3, 0},
		{"advance", 0, "", 0, 5},
		{"add", -1, "drop1", 1, 0}, // 水位=5：[-10,0) 与 [-5,5) 均已关闭 -> 整条丢弃
		{"add", -9, "drop2", 1, 0}, // 整条丢弃
		{"add", 7, "a", 1, 0},
		{"advance", 0, "", 0, 15},
		{"add", 2, "drop3", 1, 0}, // 整条丢弃
	}

	run := func() ([]WindowCount, Stats) {
		c := newTestCounter(t)
		var all []WindowCount
		for _, s := range seq {
			switch s.op {
			case "add":
				if err := c.Add(s.ts, s.key, s.count); err != nil {
					t.Fatalf("add %d %s: %v", s.ts, s.key, err)
				}
			case "advance":
				out, err := c.Advance(s.watermark)
				if err != nil {
					t.Fatalf("advance %d: %v", s.watermark, err)
				}
				all = append(all, out...)
			}
		}
		return all, c.Snapshot()
	}

	out1, st1 := run()
	out2, st2 := run()
	if !reflect.DeepEqual(out1, out2) {
		t.Fatalf("non-deterministic output:\n%+v\n%+v", out1, out2)
	}
	if st1 != st2 {
		t.Fatalf("non-deterministic stats: %+v vs %+v", st1, st2)
	}

	// 手工推算（size=10, step=5，左闭右开）：
	//  t=-7 c+1 -> [-15,-5),[-10,0);  t=-5 a+2 -> [-10,0),[-5,5);
	//  t=-3 b+1 -> [-10,0),[-5,5)
	//  advance(0) 关闭 [-15,-5) 与 [-10,0)
	//  t=2 a+1,b+3 -> [-5,5),[0,10);  advance(5) 关闭 [-5,5)
	//  t=-1 与 t=-9 的归属窗口此时全部已关闭 -> 丢弃
	//  t=7 a+1 -> [0,10),[5,15);  advance(15) 关闭 [0,10),[5,15)
	//  t=2 的归属窗口全部已关闭 -> 丢弃（共 3 条）
	want := []WindowCount{
		{WindowStart: -15, WindowEnd: -5, Key: "c", Count: 1},
		{WindowStart: -10, WindowEnd: 0, Key: "a", Count: 2},
		{WindowStart: -10, WindowEnd: 0, Key: "b", Count: 1},
		{WindowStart: -10, WindowEnd: 0, Key: "c", Count: 1},
		{WindowStart: -5, WindowEnd: 5, Key: "a", Count: 3},
		{WindowStart: -5, WindowEnd: 5, Key: "b", Count: 4},
		{WindowStart: 0, WindowEnd: 10, Key: "a", Count: 2},
		{WindowStart: 0, WindowEnd: 10, Key: "b", Count: 3},
		{WindowStart: 5, WindowEnd: 15, Key: "a", Count: 1},
	}
	if !reflect.DeepEqual(out1, want) {
		t.Fatalf("output mismatch:\ngot =%+v\nwant=%+v", out1, want)
	}
	if st1.DroppedEvents != 3 {
		t.Fatalf("DroppedEvents=%d, want 3", st1.DroppedEvents)
	}
}

func TestLogsContainInputsOutputsAndReason(t *testing.T) {
	var buf bytes.Buffer
	lg := log.New(&buf, "", 0)
	c, err := New(Config{WindowSize: 10, SlideStep: 5, MaxOpenWindows: 100, Logger: lg})
	if err != nil {
		t.Fatal(err)
	}

	if err := c.Add(0, "a", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Advance(10); err != nil {
		t.Fatal(err)
	}
	if err := c.Add(1, "a", 1); err != nil { // 整条丢弃
		t.Fatal(err)
	}
	_, rewindErr := c.Advance(9) // 时钟回退
	if !errors.Is(rewindErr, ErrClockRewind) {
		t.Fatalf("err=%v, want ErrClockRewind", rewindErr)
	}

	logs := buf.String()
	for _, want := range []string{"ADD timestamp=0", "ACCEPTED", "ADVANCE watermark=10",
		"closed 2 window(s)", "window=[-5,5)", "DROPPED", "REJECTED", "clock cannot move backwards"} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs missing %q\nlogs:\n%s", want, logs)
		}
	}
}
