package window

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// ---- 测试用日志器：捕获输入、输出与判定依据日志 ----

type bufLogger struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *bufLogger) Logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(&l.b, format+"\n", args...)
}

func (l *bufLogger) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// ---- 纯函数：窗口划分（含负时间戳与边界） ----

func TestWindowStart(t *testing.T) {
	cases := []struct {
		ts, size, want int64
	}{
		{0, 10, 0}, {9, 10, 0}, {10, 10, 10}, {19, 10, 10}, {20, 10, 20},
		{-1, 10, -10},  // [-10,0)
		{-10, 10, -10}, // 恰好落在边界，归入左侧窗口 [-10,0)
		{-11, 10, -20}, // [-20,-10)
		{-20, 10, -20},
		{5, 3, 3}, {-1, 3, -3}, {-4, 3, -6},
	}
	for _, tc := range cases {
		if got := windowStart(tc.ts, tc.size); got != tc.want {
			t.Errorf("windowStart(%d,%d)=%d, want %d", tc.ts, tc.size, got, tc.want)
		}
	}
}

// ---- 配置校验：每种非法原因可区分 ----

func TestInvalidConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want error
	}{
		{"size zero", Config{Size: 0}, ErrNonPositiveSize},
		{"size negative", Config{Size: -1}, ErrNonPositiveSize},
		{"delay negative", Config{Size: 1, Delay: -1}, ErrNegativeDelay},
		{"lateness negative", Config{Size: 1, AllowedLateness: -1}, ErrNegativeAllowedLateness},
		{"max open negative", Config{Size: 1, MaxOpenWindows: -1}, ErrInvalidMaxOpenWindows},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.cfg)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v, want %v", err, tc.want)
			}
		})
	}
	if _, err := New(Config{Size: 10}); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

// ---- 负时间戳归属、触发与清除 ----

func TestNegativeTimestampsTriggerAndClear(t *testing.T) {
	c, err := New(Config{Size: 10, AllowedLateness: 0})
	if err != nil {
		t.Fatal(err)
	}
	mustProc := func(evs ...Event) []Emission {
		t.Helper()
		out, err := c.Process(evs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return out
	}

	// [-20,-10) 两个事件，均在缓冲中。
	mustProc(Event{"k", -15}, Event{"k", -11})
	if st := c.Snapshot(); st.Watermark != -11 || len(st.Results) != 0 {
		t.Fatalf("state before fire: %+v", st)
	}

	// ts=-10 使水位线到达 -10，[-20,-10) 到期触发 count=2；
	// 容限为 0，触发同一轮即清除；-10 本身归入 [-10,0)。
	out := mustProc(Event{"k", -10})
	if len(out) != 1 || out[0].Kind != EmissionFired ||
		out[0].Result.Start != -20 || out[0].Result.End != -10 || out[0].Result.Count != 2 {
		t.Fatalf("unexpected emissions: %+v", out)
	}
	st := c.Snapshot()
	if len(st.Results) != 0 { // 已清除，不再保留结果
		t.Fatalf("cleared window should be gone, got results=%+v", st.Results)
	}
	if st.Watermark != -10 {
		t.Fatalf("watermark=%d, want -10", st.Watermark)
	}

	// 已清除窗口再来事件 -> 丢弃（wm=-10 >= gc-time=-10+0，恰好边界）。
	mustProc(Event{"k", -15})
	if st := c.Snapshot(); st.Dropped != 1 {
		t.Fatalf("dropped=%d, want 1", st.Dropped)
	}
}

// ---- 恰好落在边界上的迟到判定 ----

func TestLateBoundaryExactly(t *testing.T) {
	// size=10, delay=0, lateness=5：[0,10) 的 gc-time=15。
	c, _ := New(Config{Size: 10, AllowedLateness: 5})
	mustProc := func(evs ...Event) {
		t.Helper()
		if _, err := c.Process(evs); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	mustProc(Event{"k", 0})
	mustProc(Event{"k", 10}) // wm=10：[0,10) 触发 count=1，保留（gc=15）

	// wm=14 时迟到事件 ts=1：wm=14 < gc=15，接受并修正为 2。
	mustProc(Event{"k", 14}, Event{"k", 1})
	st := c.Snapshot()
	if st.Dropped != 0 {
		t.Fatalf("dropped=%d, want 0", st.Dropped)
	}
	r := findResult(st.Results, "k", 0)
	if r == nil || r.Count != 2 {
		t.Fatalf("corrected count=%v, want 2", r)
	}
	// 最近一次输出必须是 corrected 且计数为 2。
	last := st.Emissions[len(st.Emissions)-1]
	if last.Kind != EmissionCorrected || last.Result.Count != 2 {
		t.Fatalf("last emission=%+v, want corrected count=2", last)
	}

	// wm 到达 15（== gc-time）：窗口在同一轮被清除；之后 ts=2 恰好落在丢弃边界。
	mustProc(Event{"k", 15})
	mustProc(Event{"k", 2})
	st = c.Snapshot()
	if st.Dropped != 1 {
		t.Fatalf("dropped=%d, want 1 (wm==gc-time must drop)", st.Dropped)
	}
	if findResult(st.Results, "k", 0) != nil {
		t.Fatalf("window should be cleared at wm==gc-time: %+v", st.Results)
	}
}

// ---- 水位线：随最大事件时间推进、delay 生效、只单调前进 ----

func TestWatermarkMonotonicAndDelay(t *testing.T) {
	// delay=3：wm = maxTS - 3；[0,10) 需 maxTS>=13 才触发。
	c, _ := New(Config{Size: 10, Delay: 3, AllowedLateness: 0})
	mustProc := func(evs ...Event) {
		t.Helper()
		if _, err := c.Process(evs); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	mustProc(Event{"k", 0})  // wm=-3，缓冲
	mustProc(Event{"k", 12}) // wm=9，[0,10) 未到期
	if st := c.Snapshot(); st.Watermark != 9 || len(st.Emissions) != 0 {
		t.Fatalf("state=%+v", st)
	}
	out, _ := c.Process([]Event{{"k", 13}}) // wm=10，触发 [0,10)
	if len(out) != 1 || out[0].Result.Count != 1 {
		t.Fatalf("emissions=%+v", out)
	}
	// 乱序旧事件不回退水位线。
	mustProc(Event{"k", 1}) // wm 仍为 10；窗口已清除（容限0）-> 丢弃
	st := c.Snapshot()
	if st.Watermark != 10 || st.Dropped != 1 {
		t.Fatalf("state=%+v, want wm=10 dropped=1", st)
	}
}

// ---- 空键拒绝：不改状态，之后可继续使用 ----

func TestEmptyKeyRejectedAtomicAndReusable(t *testing.T) {
	c, _ := New(Config{Size: 10})

	_, err := c.Process([]Event{{Key: "", Timestamp: 1}})
	if !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("err=%v, want ErrEmptyKey", err)
	}
	if st := c.Snapshot(); st.WatermarkSet || st.Dropped != 0 || len(st.Results) != 0 || len(st.Emissions) != 0 {
		t.Fatalf("rejected input changed state: %+v", st)
	}

	// 合法事件与空键同批：整批拒绝，包括排在空键之前的事件也不得生效。
	_, err = c.Process([]Event{{Key: "k", Timestamp: 5}, {Key: "", Timestamp: 5}})
	if !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("err=%v, want ErrEmptyKey", err)
	}
	if st := c.Snapshot(); st.WatermarkSet || len(st.Emissions) != 0 {
		t.Fatalf("mixed batch must be all-or-nothing: %+v", st)
	}

	// 拒绝后仍可正常使用。
	if _, err := c.Process([]Event{{Key: "k", Timestamp: 5}}); err != nil {
		t.Fatalf("counter not reusable after rejection: %v", err)
	}
	if st := c.Snapshot(); !st.WatermarkSet || st.Watermark != 5 {
		t.Fatalf("state after reuse=%+v", st)
	}
}

// ---- 未结算窗口数超限：整体拒绝且状态不变 ----

func TestTooManyOpenWindows(t *testing.T) {
	// delay 很大，保证期间不会有窗口触发/清除。
	c, _ := New(Config{Size: 10, Delay: 1000, MaxOpenWindows: 1})

	if _, err := c.Process([]Event{{"k1", 0}}); err != nil {
		t.Fatalf("first window should be accepted: %v", err)
	}
	// 第二个并存窗口（不同键）超限。
	_, err := c.Process([]Event{{"k2", 20}})
	if !errors.Is(err, ErrTooManyOpenWindows) {
		t.Fatalf("err=%v, want ErrTooManyOpenWindows", err)
	}
	st := c.Snapshot()
	if st.Watermark != -1000 || len(st.Results) != 0 || st.Dropped != 0 {
		t.Fatalf("rejected batch changed state: %+v", st)
	}
	if countOpenWindows(c) != 1 {
		t.Fatalf("open windows=%d, want 1", countOpenWindows(c))
	}
	// 落入已有窗口的事件仍然接受，且不新增窗口。
	if _, err := c.Process([]Event{{"k1", 5}}); err != nil {
		t.Fatalf("same-window event rejected: %v", err)
	}
	if countOpenWindows(c) != 1 {
		t.Fatalf("open windows=%d, still want 1", countOpenWindows(c))
	}
}

// ---- 批次内“先丢弃再非法”也必须整体回滚 ----

func TestBatchRollbackIncludesDropped(t *testing.T) {
	c, _ := New(Config{Size: 10})
	// ts=10 建立 [10,20) 并把 wm 推到 10；ts=0 会被丢弃（gc=10==wm）；
	// 随后的空键导致整批拒绝：丢弃计数也不得增加。
	_, err := c.Process([]Event{{"k", 10}, {"k", 0}, {"", 0}})
	if !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("err=%v", err)
	}
	st := c.Snapshot()
	if st.WatermarkSet || st.Dropped != 0 || countOpenWindows(c) != 0 {
		t.Fatalf("batch must roll back watermark/dropped/open: %+v", st)
	}
}

// ---- 可复现：同一输入序列反复计算，输出完全一致；多键顺序确定 ----

func TestDeterministicReplay(t *testing.T) {
	seq := []Event{
		{"b", 0}, {"a", 0}, {"a", 11}, {"b", 10},
		{"a", 2}, {"b", 21}, {"a", -5}, {"b", 3},
	}
	cfg := Config{Size: 10, Delay: 1, AllowedLateness: 7}

	run := func() State {
		var log bufLogger
		cfg.Logger = &log
		c, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		for _, ev := range seq {
			if _, err := c.Process([]Event{ev}); err != nil {
				t.Fatal(err)
			}
		}
		st := c.Snapshot()
		if len(log.String()) == 0 {
			t.Fatal("logger received no output")
		}
		return st
	}

	s1, s2 := run(), run()
	if !reflect.DeepEqual(s1, s2) {
		t.Fatalf("replays differ:\n%+v\n%+v", s1, s2)
	}

	// 同批多键在水位线=10 时同时触发 [0,10)，输出必须按键名字典序：a 先于 b。
	c, _ := New(Config{Size: 10})
	if _, err := c.Process([]Event{{"b", 0}, {"a", 0}}); err != nil {
		t.Fatal(err)
	}
	out, err := c.Process([]Event{{"b", 10}})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) < 2 || out[0].Result.Key != "a" || out[1].Result.Key != "b" {
		t.Fatalf("emission order not deterministic by key: %+v", out)
	}
}

// ---- 日志必须包含输入、输出与判定依据 ----

func TestLoggerContents(t *testing.T) {
	var log bufLogger
	c, _ := New(Config{Size: 10, AllowedLateness: 1, Logger: &log})
	if _, err := c.Process([]Event{{"k", 0}, {"k", 10}, {"k", 1}, {"k", 12}}); err != nil {
		t.Fatal(err)
	}
	s := log.String()
	for _, want := range []string{"input", "output", "watermark", "reason"} {
		if !bytes.Contains([]byte(s), []byte(want)) {
			t.Errorf("log missing %q\n%s", want, s)
		}
	}
}

// ---- 被拒批次不得在日志中留下幻影的输入/水位线/丢弃记录 ----

func TestRejectedBatchLeavesNoLogs(t *testing.T) {
	var log bufLogger
	c, _ := New(Config{Size: 10, Logger: &log})
	if _, err := c.Process([]Event{{"k", 5}}); err != nil {
		t.Fatal(err)
	}
	before := log.String()

	// 合法事件 + 空键同批：整批拒绝，合法事件的处理细节不得出现在日志里。
	if _, err := c.Process([]Event{{"k", 7}, {"", 8}}); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("err=%v, want ErrEmptyKey", err)
	}
	after := log.String()

	// 只允许新增一行 reject 记录。
	added := strings.TrimPrefix(after, before)
	lines := strings.Split(strings.TrimRight(added, "\n"), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "reject") {
		t.Fatalf("rejected batch leaked logs: %q", added)
	}
}

// ---- 并发只读一致 + 与写入并发时无竞态 ----

func TestConcurrentSnapshots(t *testing.T) {
	c, _ := New(Config{Size: 10, AllowedLateness: 100})
	if _, err := c.Process([]Event{{"k", 0}, {"k2", 3}}); err != nil { // 两个窗口
		t.Fatal(err)
	}

	const n = 50
	states := make([]State, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			states[i] = c.Snapshot()
		}(i)
	}
	close(start)
	wg.Wait()
	for i := 1; i < n; i++ {
		if !reflect.DeepEqual(states[0], states[i]) {
			t.Fatalf("concurrent snapshot %d differs", i)
		}
	}

	// 写入与读取并发：竞态由 -race 检测；每个快照自身必须自洽。
	var stop sync.WaitGroup
	quit := make(chan struct{})
	stop.Add(2)
	go func() { // writer
		defer stop.Done()
		var ts int64
		for {
			select {
			case <-quit:
				return
			default:
				_, _ = c.Process([]Event{{"k", ts}, {"k2", ts + 3}})
				ts++
			}
		}
	}()
	go func() { // reader
		defer stop.Done()
		for {
			select {
			case <-quit:
				return
			default:
				st := c.Snapshot()
				if !sortedResults(st.Results) {
					t.Errorf("results not sorted: %+v", st.Results)
				}
				for j := range st.Emissions {
					if st.Emissions[j].Seq != int64(j)+1 {
						t.Errorf("emission seq gap: %+v", st.Emissions[j])
					}
				}
			}
		}
	}()
	// 让并发跑一小段。
	for i := 0; i < 200; i++ {
		_, _ = c.Process([]Event{{"k", 1000 + int64(i)}})
	}
	close(quit)
	stop.Wait()
}

// ---- 辅助 ----

func findResult(rs []WindowResult, key string, start int64) *WindowResult {
	for i := range rs {
		if rs[i].Key == key && rs[i].Start == start {
			return &rs[i]
		}
	}
	return nil
}

func sortedResults(rs []WindowResult) bool {
	for i := 1; i < len(rs); i++ {
		if rs[i-1].Key > rs[i].Key ||
			(rs[i-1].Key == rs[i].Key && rs[i-1].Start >= rs[i].Start) {
			return false
		}
	}
	return true
}

func countOpenWindows(c *Counter) int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.openN
}
