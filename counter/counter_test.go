package counter

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func mustNew(t *testing.T, cfg Config, opts ...Option) *Counter {
	t.Helper()
	c, err := New(cfg, opts...)
	if err != nil {
		t.Fatalf("New(%+v) unexpected error: %v", cfg, err)
	}
	return c
}

func addOK(t *testing.T, c *Counter, key string, ts int64) []Result {
	t.Helper()
	out, err := c.Add(Event{Key: key, Timestamp: ts})
	if err != nil {
		t.Fatalf("Add(%q,%d) unexpected error: %v", key, ts, err)
	}
	return out
}

// TestAccumulativeOutputs 验证主流程：事件计入所有终点晚于它的子窗口，
// 输出为从大窗口起点到当前终点的累计计数；含大窗口边界事件 ts=10/20。
func TestAccumulativeOutputs(t *testing.T) {
	c := mustNew(t, Config{WindowSize: 10, Step: 5, MaxWindows: 4}, WithLogger(io.Discard))

	type step struct {
		ts   int64
		want []Result // 本次 Add 新触发的结果
	}
	steps := []step{
		{0, nil},
		{3, nil},
		{4, nil},
		{5, []Result{{"a", 0, 5, 3}}}, // 桶 [0,5) 三个事件
		{9, nil},
		{10, []Result{{"a", 0, 10, 5}}}, // 边界事件归新窗口；旧窗口累计 5
		{15, []Result{{"a", 10, 15, 1}}},
		{19, nil},
		{20, []Result{{"a", 10, 20, 3}}}, // 10..19 共 3 个事件
	}
	for _, s := range steps {
		got := addOK(t, c, "a", s.ts)
		if !reflect.DeepEqual(got, s.want) {
			t.Errorf("Add(a,%d) fresh=%v, want %v", s.ts, got, s.want)
		}
	}

	want := []Result{
		{"a", 0, 5, 3},
		{"a", 0, 10, 5},
		{"a", 10, 15, 1},
		{"a", 10, 20, 3},
	}
	if st := c.Snapshot(); !reflect.DeepEqual(st.Outputs, want) {
		t.Fatalf("outputs=%v, want %v", st.Outputs, want)
	} else if st.Dropped != 0 || st.Watermark != 20 {
		t.Fatalf("state=%+v, want watermark=20 dropped=0", st)
	}
}

// TestZeroCountEmittedAndDropBoundary 验证计数为零也输出，以及丢弃规则：
// 最小子窗口终点“不超过（<=）”水位线即丢弃，包含恰好相等的边界。
func TestZeroCountEmittedAndDropBoundary(t *testing.T) {
	c := mustNew(t, Config{10, 5, 3}, WithLogger(io.Discard))

	// 首个事件在 ts=6：它不计入终点 5（终点不晚于事件时间），
	// 水位线 6 已使终点 5 到期，必须输出零计数。
	if got := addOK(t, c, "b", 6); !reflect.DeepEqual(got, []Result{{"b", 0, 5, 0}}) {
		t.Fatalf("zero-count emit=%v", got)
	}
	addOK(t, c, "b", 10) // 推进水位线到 10，窗口 [0,10) 全部触发
	wantAfter10 := []Result{{"b", 0, 5, 0}, {"b", 0, 10, 1}}
	if got := c.Outputs(); !reflect.DeepEqual(got, wantAfter10) {
		t.Fatalf("outputs=%v, want %v", got, wantAfter10)
	}

	// 水位线为 10：ts=4/5/9 的最小子窗口终点分别为 5/10/10，均 <= 10，丢弃。
	for _, ts := range []int64{4, 5, 9} {
		if out, err := c.Add(Event{"b", ts}); err != nil || out != nil {
			t.Fatalf("Add(b,%d) expect drop, got out=%v err=%v", ts, out, err)
		}
	}
	if st := c.Snapshot(); st.Dropped != 3 {
		t.Fatalf("dropped=%d, want 3", st.Dropped)
	} else if !reflect.DeepEqual(st.Outputs, wantAfter10) {
		t.Fatalf("late events must not change outputs: %v", st.Outputs)
	}

	// ts=11 的最小终点为 15 > 10，正常接收。
	addOK(t, c, "b", 11)
	addOK(t, c, "b", 15)
	want := append(wantAfter10, Result{"b", 10, 15, 2}) // b@10 与 b@11 都在桶 [10,15)
	if got := c.Outputs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("outputs=%v, want %v", got, want)
	}
}

// TestWatermarkJumpEmitsAllZeroEnds 验证水位线一次跳跃跨过多个子窗口终点时，
// 中间所有终点（即使没有事件）都各触发一次零计数，且严格按步长连续。
func TestWatermarkJumpEmitsAllZeroEnds(t *testing.T) {
	// size=10 step=2 => 5 个子窗口，终点 2,4,6,8,10。
	c := mustNew(t, Config{10, 2, 2}, WithLogger(io.Discard))
	got := addOK(t, c, "z", 9) // 水位线直接到 9，终点 2,4,6,8 全部到期
	want := []Result{
		{"z", 0, 2, 0},
		{"z", 0, 4, 0},
		{"z", 0, 6, 0},
		{"z", 0, 8, 0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("jump emits=%v, want %v", got, want)
	}
	// 再推进到 10，最后一个终点到期，累计计数为 1（z@9 落在桶 [8,10)）。
	got = addOK(t, c, "z", 10)
	if !reflect.DeepEqual(got, []Result{{"z", 0, 10, 1}}) {
		t.Fatalf("final emit=%v", got)
	}
}

func TestNegativeTimestamps(t *testing.T) {
	c := mustNew(t, Config{10, 5, 3}, WithLogger(io.Discard))

	// [-10,0) 窗口：事件 -10/-6 落在桶 [-10,-5)，-5/-1 落在桶 [-5,0)。
	addOK(t, c, "n", -10) // 边界事件 -10 归 [-10,0)
	addOK(t, c, "n", -6)
	if got := addOK(t, c, "n", -5); !reflect.DeepEqual(got, []Result{{"n", -10, -5, 2}}) {
		t.Fatalf("emit at -5: %v", got)
	}
	addOK(t, c, "n", -1)
	if got := addOK(t, c, "n", 0); !reflect.DeepEqual(got, []Result{{"n", -10, 0, 4}}) {
		t.Fatalf("emit at 0: %v", got) // 负窗口累计 4 个事件
	}

	// 水位线已到 0：负时间戳迟到事件一律丢弃。
	for _, ts := range []int64{-10, -1} {
		if _, err := c.Add(Event{"n", ts}); err != nil {
			t.Fatalf("Add(n,%d) unexpected error: %v", ts, err)
		}
	}
	if st := c.Snapshot(); st.Dropped != 2 {
		t.Fatalf("dropped=%d, want 2", st.Dropped)
	}
}

// TestInvalidConfig 覆盖各类非法构造参数。
func TestInvalidConfig(t *testing.T) {
	bad := []Config{
		{0, 5, 1},
		{-10, 5, 1},
		{10, 0, 1},
		{10, -5, 1},
		{10, 3, 1},  // step 不整除 window size
		{10, 20, 1}, // step 大于 window size
		{10, 5, 0},
		{10, 5, -1},
	}
	for _, cfg := range bad {
		_, err := New(cfg)
		var r *RejectError
		if !errors.As(err, &r) || r.Code != ReasonInvalidConfig {
			t.Errorf("New(%+v) err=%v, want RejectError{%s}", cfg, err, ReasonInvalidConfig)
		}
		if !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("New(%+v) err should errors.Is ErrInvalidConfig", cfg)
		}
	}
}

// TestRejectionDoesNotMutateState 验证空键与窗口超限被拒后，
// 水位线、丢弃数、已输出结果逐项不变（已完成窗口仍计入保留数）。
func TestRejectionDoesNotMutateState(t *testing.T) {
	c := mustNew(t, Config{10, 5, 2}, WithLogger(io.Discard))
	addOK(t, c, "x", 0)
	addOK(t, c, "x", 10) // 保留窗口 0、10（窗口 0 已完成但仍保留），达到上限 2
	before := c.Snapshot()

	// 新建第 3 个大窗口 => 超限拒绝。
	_, err := c.Add(Event{"x", 20})
	var r *RejectError
	if !errors.As(err, &r) || r.Code != ReasonTooManyWindows {
		t.Fatalf("expect %s, got %v", ReasonTooManyWindows, err)
	}
	if !errors.Is(err, ErrTooManyWindows) {
		t.Fatal("reject should errors.Is ErrTooManyWindows")
	}

	// 空键拒绝（时间戳即使很新也不得推进水位线）。
	_, err = c.Add(Event{"", 100})
	if !errors.As(err, &r) || r.Code != ReasonEmptyKey {
		t.Fatalf("expect %s, got %v", ReasonEmptyKey, err)
	}

	after := c.Snapshot()
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("state changed after rejection:\nbefore=%+v\nafter =%+v", before, after)
	}
}

// TestDeterministicOrder 验证同一输入序列反复计算输出完全相同（确定性），
// 输出多重集合符合预期，且每个 (key,大窗口) 内子窗口终点严格递增、
// 累计计数非降。
func TestDeterministicOrder(t *testing.T) {
	seq := []Event{
		{"b", 0}, {"a", 1}, {"c", 2},
		{"a", 10}, {"b", 11}, {"c", 12},
		{"a", 5}, {"b", 20}, {"a", 21},
	}
	run := func() State {
		c := mustNew(t, Config{10, 5, 5}, WithLogger(io.Discard))
		for _, e := range seq {
			if _, err := c.Add(e); err != nil {
				t.Fatalf("Add(%v): %v", e, err)
			}
		}
		return c.Snapshot()
	}
	s1, s2 := run(), run()
	if !reflect.DeepEqual(s1, s2) {
		t.Fatalf("non-deterministic:\n%+v\nvs\n%+v", s1, s2)
	}

	// 期望的输出多重集合（与追加顺序无关地核对内容）。
	// 说明：a@5 在 wm=12 时到达，firstEnd=10 <= 12，被丢弃；
	// 故每个 (key,大窗口) 最终都只有 1 个事件，两次触发计数均为 1。
	want := []Result{
		{"a", 0, 5, 1}, {"a", 0, 10, 1},
		{"b", 0, 5, 1}, {"b", 0, 10, 1},
		{"c", 0, 5, 1}, {"c", 0, 10, 1},
		{"a", 10, 15, 1}, {"a", 10, 20, 1},
		{"b", 10, 15, 1}, {"b", 10, 20, 1},
		{"c", 10, 15, 1}, {"c", 10, 20, 1},
	}
	less := func(s []Result) func(i, j int) bool {
		return func(i, j int) bool {
			x, y := s[i], s[j]
			if x.Key != y.Key {
				return x.Key < y.Key
			}
			if x.WindowStart != y.WindowStart {
				return x.WindowStart < y.WindowStart
			}
			return x.End < y.End
		}
	}
	gotSorted := append([]Result(nil), s1.Outputs...)
	sort.Slice(gotSorted, less(gotSorted))
	wantSorted := append([]Result(nil), want...)
	sort.Slice(wantSorted, less(wantSorted))
	if !reflect.DeepEqual(gotSorted, wantSorted) {
		t.Fatalf("output multiset=%v, want %v", gotSorted, wantSorted)
	}

	// 每个 (key,start) 子序列：终点严格递增，累计计数非降。
	type ks struct {
		key string
		s   int64
	}
	seen := map[ks]int64{}
	prevEnd := map[ks]int64{}
	for _, r := range s1.Outputs {
		k := ks{r.Key, r.WindowStart}
		if e, ok := prevEnd[k]; ok && r.End <= e {
			t.Fatalf("window %v ends not increasing: %d then %d", k, e, r.End)
		}
		prevEnd[k] = r.End
		if r.Count < seen[k] {
			t.Fatalf("window %v cumulative count decreased: %d then %d", k, seen[k], r.Count)
		}
		seen[k] = r.Count
	}
}

// TestSnapshotIsolation 验证快照是深拷贝，后续写入不影响旧快照。
func TestSnapshotIsolation(t *testing.T) {
	c := mustNew(t, Config{10, 5, 3}, WithLogger(io.Discard))
	addOK(t, c, "a", 5)
	s1 := c.Snapshot()
	addOK(t, c, "a", 10)
	addOK(t, c, "a", 15)
	want := []Result{{"a", 0, 5, 0}}
	if !reflect.DeepEqual(s1.Outputs, want) {
		t.Fatalf("old snapshot mutated: %v, want %v", s1.Outputs, want)
	}
	if s1.Watermark != 5 || s1.Dropped != 0 {
		t.Fatalf("old snapshot fields changed: %+v", s1)
	}
}

// TestInitialStateAndAccessors 验证空计数器的初始状态：水位线“未设置”，
// 并覆盖 Watermark/Dropped/Outputs 便捷读取方法及深拷贝语义。
func TestInitialStateAndAccessors(t *testing.T) {
	c := mustNew(t, Config{10, 5, 2}, WithLogger(io.Discard))

	if got := c.Watermark(); got != math.MinInt64 {
		t.Fatalf("initial watermark=%d, want MinInt64", got)
	}
	if c.Dropped() != 0 {
		t.Fatalf("initial dropped=%d, want 0", c.Dropped())
	}
	if got := c.Outputs(); len(got) != 0 {
		t.Fatalf("initial outputs=%v, want empty", got)
	}
	st := c.Snapshot()
	if st.WatermarkSet || st.Watermark != math.MinInt64 {
		t.Fatalf("initial snapshot=%+v, want unset watermark", st)
	}

	// 接收一条后，便捷方法与快照一致。
	addOK(t, c, "a", 7)
	if c.Watermark() != 7 {
		t.Fatalf("watermark=%d, want 7", c.Watermark())
	}
	if got := c.Outputs(); !reflect.DeepEqual(got, []Result{{"a", 0, 5, 0}}) {
		t.Fatalf("outputs=%v", got)
	}
}

// TestRejectErrorIs 覆盖 errors.Is 的匹配与不匹配分支。
func TestRejectErrorIs(t *testing.T) {
	e := &RejectError{Code: ReasonEmptyKey, Reason: "x"}
	if !errors.Is(e, ErrEmptyKey) {
		t.Fatal("should match ErrEmptyKey")
	}
	if errors.Is(e, ErrInvalidConfig) {
		t.Fatal("empty key must not match invalid config")
	}
	if errors.Is(e, errSentinel) {
		t.Fatal("non-RejectError target must not match")
	}
}

var errSentinel = errors.New("other")

// TestLogging 验证日志包含输入、输出与判定依据。
func TestLogging(t *testing.T) {
	var buf bytes.Buffer
	c := mustNew(t, Config{10, 5, 3}, WithLogger(&buf))
	addOK(t, c, "a", 0)
	addOK(t, c, "a", 5) // 触发 end=5
	addOK(t, c, "a", 1) // 最小终点 5 <= 水位线 5，丢弃
	logText := buf.String()
	t.Logf("counter log:\n%s", logText)
	for _, want := range []string{"input", "ACCEPT", "output", "EMIT", "DROP", "watermark"} {
		if !strings.Contains(logText, want) {
			t.Errorf("log missing %q", want)
		}
	}
}

// TestConcurrent 并发写入与快照读取：竞态由 -race 检测，
// 并校验计数不变量：被接收事件总数 == 各窗口现存计数 + 已完成窗口的最终累计。
func TestConcurrent(t *testing.T) {
	c := mustNew(t, Config{100, 10, 100}, WithLogger(io.Discard))
	var accepted int64
	stop := make(chan struct{})
	var wgR, wgW sync.WaitGroup

	// 读者：持续取快照，验证字段自洽。
	wgR.Add(1)
	go func() {
		defer wgR.Done()
		for {
			select {
			case <-stop:
				return
			default:
				st := c.Snapshot()
				if st.Outputs == nil || st.Dropped < 0 {
					t.Errorf("bad snapshot: %+v", st)
					return
				}
				if st.WatermarkSet && st.Watermark == math.MinInt64 {
					t.Errorf("watermark set but sentinel: %+v", st)
					return
				}
			}
		}
	}()

	// 写者：每个 goroutine 独占一个 key（跨 key 的乱序会产生丢弃，属正常）。
	// 用包内 addClassified 精确区分接收 / 丢弃 / 拒绝，避免并发下轮询
	// 全局 Dropped 计数造成的误分类。
	const writers = 8
	var rejected int64
	for w := 0; w < writers; w++ {
		wgW.Add(1)
		go func(w int) {
			defer wgW.Done()
			key := fmt.Sprintf("k%d", w)
			for i := 0; i < 200; i++ {
				ts := int64(w*1000 + i)
				_, disp, err := c.addClassified(Event{key, ts})
				switch {
				case err != nil:
					atomic.AddInt64(&rejected, 1)
				case disp == dispDropped:
					// 被丢弃：不计入 accepted
				default:
					atomic.AddInt64(&accepted, 1)
				}
			}
		}(w)
	}

	wgW.Wait()
	close(stop)
	wgR.Wait()

	st := c.Snapshot()
	c.mu.RLock()
	var accounted int64
	for key, byStart := range c.windows {
		for s, ws := range byStart {
			if ws.counts != nil {
				for _, n := range ws.counts {
					accounted += n
				}
			} else {
				// 已完成窗口：取该窗口最后一次输出的累计值。
				var final int64
				for _, r := range c.outputs {
					if r.Key == key && r.WindowStart == s {
						final = r.Count
					}
				}
				accounted += final
			}
		}
	}
	c.mu.RUnlock()

	// 被接收事件要么在现存桶里，要么体现在已完成窗口的最终累计中。
	if accepted != accounted {
		t.Fatalf("invariant: accepted=%d != accounted=%d (dropped=%d rejected=%d)",
			accepted, accounted, st.Dropped, atomic.LoadInt64(&rejected))
	}
	if total := accepted + st.Dropped + rejected; total != writers*200 {
		t.Fatalf("ledger: accepted=%d+dropped=%d+rejected=%d=%d != total=%d",
			accepted, st.Dropped, rejected, total, writers*200)
	}
}
