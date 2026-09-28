package hoppingwindow

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	sec = time.Second
)

// secAt 返回 Unix 纪元起第 n 秒（n 可为负）。
func secAt(n int64) time.Time { return time.Unix(n, 0) }

// ---------------------------------------------------------------------------
// 配置校验
// ---------------------------------------------------------------------------

func TestNewInvalidConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"zero window size", Config{WindowSize: 0, Slide: sec, MaxOpenWindows: 2}},
		{"negative window size", Config{WindowSize: -sec, Slide: sec, MaxOpenWindows: 2}},
		{"zero slide", Config{WindowSize: 10 * sec, Slide: 0, MaxOpenWindows: 2}},
		{"negative slide", Config{WindowSize: 10 * sec, Slide: -sec, MaxOpenWindows: 2}},
		{"slide larger than size", Config{WindowSize: 5 * sec, Slide: 6 * sec, MaxOpenWindows: 2}},
		{"zero max open windows", Config{WindowSize: 10 * sec, Slide: 5 * sec, MaxOpenWindows: 0}},
		{"negative max open windows", Config{WindowSize: 10 * sec, Slide: 5 * sec, MaxOpenWindows: -1}},
		{"max open windows below overlap", Config{WindowSize: 10 * sec, Slide: 3 * sec, MaxOpenWindows: 3}}, // ceil(10/3)=4
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := New(tc.cfg)
			if c != nil || err == nil {
				t.Fatalf("expected error, got counter=%v err=%v", c, err)
			}
			if !errorIs(err, ErrInvalidConfig) {
				t.Fatalf("expected ErrInvalidConfig, got %v", err)
			}
			var e *Error
			if !asError(err, &e) || e.Kind != KindInvalidConfig {
				t.Fatalf("expected KindInvalidConfig, got %v", err)
			}
		})
	}
}

func TestNewValidConfig(t *testing.T) {
	if _, err := New(Config{WindowSize: 10 * sec, Slide: 5 * sec, MaxOpenWindows: 2}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 非整除关系：ceil(10/3)=4，上限 4 合法。
	if _, err := New(Config{WindowSize: 10 * sec, Slide: 3 * sec, MaxOpenWindows: 4}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 窗口归属：边界、负时间戳
// ---------------------------------------------------------------------------

func TestEventExactlyOnWindowStart(t *testing.T) {
	// size=10s, slide=5s。t=0 恰好是窗口起点：
	// 应属于 [-5,5) 与 [0,10)，不属于 [-10,0)（左闭右开）。
	c, err := New(Config{WindowSize: 10 * sec, Slide: 5 * sec, MaxOpenWindows: 4})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Record(secAt(0), "a"); err != nil {
		t.Fatal(err)
	}
	out, err := c.Advance(secAt(0))
	if err != nil {
		t.Fatal(err)
	}
	// 终点 <= 0 的只有 [-10,0)，而该事件不应计入它。
	if len(out) != 0 {
		t.Fatalf("event on boundary must not belong to [-10,0), got %v", out)
	}
	out, err = c.Advance(secAt(5))
	if err != nil {
		t.Fatal(err)
	}
	// [-5,5) 在 t=5 关闭，计数应为 1；[0,10) 仍开。
	if len(out) != 1 || out[0] != (WindowResult{WindowStart: secAt(-5), WindowEnd: secAt(5), Key: "a", Count: 1}) {
		t.Fatalf("unexpected output: %+v", out)
	}
}

func TestNegativeTimestampMembership(t *testing.T) {
	// t=-7：包含它的窗口为 [-15,-5) 与 [-10,0)。
	c, _ := New(Config{WindowSize: 10 * sec, Slide: 5 * sec, MaxOpenWindows: 4})
	if err := c.Record(secAt(-7), "k"); err != nil {
		t.Fatal(err)
	}
	out, err := c.Advance(secAt(0))
	if err != nil {
		t.Fatal(err)
	}
	// 到 t=0 时 [-15,-5) 与 [-10,0) 均已到期，按终点升序输出。
	want := []WindowResult{
		{WindowStart: secAt(-15), WindowEnd: secAt(-5), Key: "k", Count: 1},
		{WindowStart: secAt(-10), WindowEnd: secAt(0), Key: "k", Count: 1},
	}
	if !resultsEqual(out, want) {
		t.Fatalf("negative timestamp membership wrong:\n got %v\nwant %v", out, want)
	}
}

func TestEventBelongsToAllOverlappingWindows(t *testing.T) {
	// size=10, slide=3（非整除），t=7：满足 7-10 < start <= 7 的 3 的倍数起点：
	// 0,3,6 → 三个窗口。
	c, _ := New(Config{WindowSize: 10 * sec, Slide: 3 * sec, MaxOpenWindows: 4})
	if err := c.Record(secAt(7), "a"); err != nil {
		t.Fatal(err)
	}
	snap := c.Snapshot()
	if len(snap.WindowCounts) != 3 {
		t.Fatalf("event should belong to 3 windows, snapshot=%+v", snap)
	}
	gotStarts := map[int64]bool{}
	for _, w := range snap.WindowCounts {
		gotStarts[w.WindowStart.Unix()] = true
		if w.Count != 1 {
			t.Fatalf("count should be 1 in each window, got %d", w.Count)
		}
	}
	for _, s := range []int64{0, 3, 6} {
		if !gotStarts[s] {
			t.Fatalf("missing window starting at %d in %v", s, gotStarts)
		}
	}
}

// ---------------------------------------------------------------------------
// 部分迟到与整条丢弃
// ---------------------------------------------------------------------------

func TestPartialLateAndFullDrop(t *testing.T) {
	c, _ := New(Config{WindowSize: 10 * sec, Slide: 5 * sec, MaxOpenWindows: 4})

	// 先把时钟推进到 0：[-10,0) 已关闭，[-5,5) 仍打开。
	if _, err := c.Advance(secAt(0)); err != nil {
		t.Fatal(err)
	}
	// t=-2 的迟到事件：[-10,0) 已关闭不计入，[-5,5) 仍打开计入 1 次。
	if err := c.Record(secAt(-2), "late"); err != nil {
		t.Fatal(err)
	}
	snap := c.Snapshot()
	if len(snap.WindowCounts) != 1 ||
		snap.WindowCounts[0].WindowStart.Unix() != -5 ||
		snap.WindowCounts[0].Count != 1 {
		t.Fatalf("partial late event should land only in [-5,5), got %+v", snap)
	}
	if snap.Dropped != 0 {
		t.Fatalf("dropped should be 0, got %d", snap.Dropped)
	}

	// 再推进到 5 取出 [-5,5)。
	out, err := c.Advance(secAt(5))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Count != 1 || out[0].Key != "late" {
		t.Fatalf("late event count wrong: %v", out)
	}

	// 时钟到 100 后，t=-2 的事件没有任何未关闭窗口可去 → 整条丢弃。
	if _, err := c.Advance(secAt(100)); err != nil {
		t.Fatal(err)
	}
	if err := c.Record(secAt(-2), "late"); err != nil {
		t.Fatal(err)
	}
	snap = c.Snapshot()
	if snap.Dropped != 1 {
		t.Fatalf("fully late event should be dropped once, dropped=%d", snap.Dropped)
	}
	if len(snap.WindowCounts) != 0 {
		t.Fatalf("no open windows expected, got %+v", snap.WindowCounts)
	}
}

// ---------------------------------------------------------------------------
// 时钟推进与关闭顺序：按窗口终点、再按键；每窗口只输出一次
// ---------------------------------------------------------------------------

func TestAdvanceClosingOrder(t *testing.T) {
	c, _ := New(Config{WindowSize: 10 * sec, Slide: 5 * sec, MaxOpenWindows: 8})
	mustRecord := func(ts int64, key string) {
		t.Helper()
		if err := c.Record(secAt(ts), key); err != nil {
			t.Fatal(err)
		}
	}
	// 构造多个窗口与键：
	// t=0 -> [-5,5){b}, [0,10){b}
	mustRecord(0, "b")
	// t=6 -> [0,10){a,b}, [5,15){a}
	mustRecord(6, "a")

	out, err := c.Advance(secAt(15))
	if err != nil {
		t.Fatal(err)
	}
	want := []WindowResult{
		{WindowStart: secAt(-5), WindowEnd: secAt(5), Key: "b", Count: 1},
		{WindowStart: secAt(0), WindowEnd: secAt(10), Key: "a", Count: 1},
		{WindowStart: secAt(0), WindowEnd: secAt(10), Key: "b", Count: 1},
		{WindowStart: secAt(5), WindowEnd: secAt(15), Key: "a", Count: 1},
	}
	if !resultsEqual(out, want) {
		t.Fatalf("closing order wrong:\n got %v\nwant %v", out, want)
	}

	// 再次推进不能重复输出已关闭窗口。
	out, err = c.Advance(secAt(100))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Fatalf("closed windows must be emitted exactly once, got %v", out)
	}
}

func TestAdvanceInStepsSameOutputAsBigJump(t *testing.T) {
	build := func() (*Counter, []WindowResult) {
		c, _ := New(Config{WindowSize: 10 * sec, Slide: 5 * sec, MaxOpenWindows: 8})
		_ = c.Record(secAt(0), "a")
		_ = c.Record(secAt(6), "a")
		_ = c.Record(secAt(6), "b")
		var all []WindowResult
		for _, tt := range []int64{5, 10, 15} {
			out, err := c.Advance(secAt(tt))
			if err != nil {
				t.Fatal(err)
			}
			all = append(all, out...)
		}
		return c, all
	}
	_, stepped := build()

	c2, _ := New(Config{WindowSize: 10 * sec, Slide: 5 * sec, MaxOpenWindows: 8})
	_ = c2.Record(secAt(0), "a")
	_ = c2.Record(secAt(6), "a")
	_ = c2.Record(secAt(6), "b")
	jumped, err := c2.Advance(secAt(15))
	if err != nil {
		t.Fatal(err)
	}
	if !resultsEqual(stepped, jumped) {
		t.Fatalf("stepwise vs one-shot advance differ:\nstep=%v\njump=%v", stepped, jumped)
	}
}

// ---------------------------------------------------------------------------
// 非法输入：空键、时钟回退、打开窗口超限；且拒绝不得改变任何状态
// ---------------------------------------------------------------------------

func TestRejectedOpsDoNotMutateState(t *testing.T) {
	// size=6, slide=2，单事件最多 3 个窗口，上限恰好 3。
	c, _ := New(Config{WindowSize: 6 * sec, Slide: 2 * sec, MaxOpenWindows: 3})
	_ = c.Record(secAt(0), "x")  // 打开 [-4,2),[-2,4),[0,6)
	_, _ = c.Advance(secAt(-10)) // 无关窗口不被关闭；时钟来到 -10

	before := c.Snapshot()

	// 1) 空键
	if err := c.Record(secAt(0), ""); !errorIs(err, ErrEmptyKey) {
		t.Fatalf("expected ErrEmptyKey, got %v", err)
	}
	// 2) 时钟回退
	if _, err := c.Advance(secAt(-11)); !errorIs(err, ErrClockRegression) {
		t.Fatalf("expected ErrClockRegression, got %v", err)
	}
	// 3) 打开窗口超限：t=10 会引入 [6,12),[8,14),[10,16) 三个全新窗口 → 6 > 3
	if err := c.Record(secAt(10), "y"); !errorIs(err, ErrTooManyOpenWindows) {
		t.Fatalf("expected ErrTooManyOpenWindows, got %v", err)
	}

	after := c.Snapshot()
	if !snapshotsEqual(before, after) {
		t.Fatalf("rejected operations mutated state:\nbefore=%+v\nafter =%+v", before, after)
	}
	if after.Dropped != 0 {
		t.Fatalf("rejected operations must not change dropped count, got %d", after.Dropped)
	}
	// 已输出结果不重复：再推进一次，仍只看到原先三个窗口。
	out, err := c.Advance(secAt(2))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 { // 只有 [-4,2) 到期
		t.Fatalf("exactly one window should close at t=2, got %v", out)
	}
}

func TestAdvanceToSameTimeIsNoop(t *testing.T) {
	c, _ := New(Config{WindowSize: 10 * sec, Slide: 5 * sec, MaxOpenWindows: 4})
	_ = c.Record(secAt(0), "a")
	out1, _ := c.Advance(secAt(0))
	out2, err := c.Advance(secAt(0))
	if err != nil {
		t.Fatalf("advancing to same time is not regression: %v", err)
	}
	if len(out1) != 0 || len(out2) != 0 {
		t.Fatalf("same-time advance must close nothing, got %v %v", out1, out2)
	}
}

func TestTooManyWindowsRecoversAfterAdvance(t *testing.T) {
	c, _ := New(Config{WindowSize: 6 * sec, Slide: 2 * sec, MaxOpenWindows: 3})
	_ = c.Record(secAt(0), "x")
	if err := c.Record(secAt(10), "y"); !errorIs(err, ErrTooManyOpenWindows) {
		t.Fatalf("expected ErrTooManyOpenWindows, got %v", err)
	}
	// 关闭全部旧窗口后，新事件可正常计入。
	if _, err := c.Advance(secAt(6)); err != nil {
		t.Fatal(err)
	}
	if err := c.Record(secAt(10), "y"); err != nil {
		t.Fatalf("expected record to succeed after freeing windows, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// 并发：并发写入与朴素参照一致；并发读取一致；重复执行确定性
// ---------------------------------------------------------------------------

// naiveCounter 是用最直白方式实现的规格参照：遍历所有已知窗口起点做区间判断。
type naiveCounter struct {
	size, slide int64
	starts      []int64
	counts      map[int64]map[string]int64
}

func newNaive(size, slide time.Duration) *naiveCounter {
	return &naiveCounter{
		size:   int64(size),
		slide:  int64(slide),
		counts: map[int64]map[string]int64{},
	}
}

func (n *naiveCounter) ensureStart(s int64) {
	if _, ok := n.counts[s]; !ok {
		n.counts[s] = map[string]int64{}
		n.starts = append(n.starts, s)
	}
}

func (n *naiveCounter) record(ts time.Time, key string) {
	t := ts.UnixNano()
	// 直接枚举 k 的可能范围（与实现不同的算法路径），按区间定义判断。
	lo := int64(math.Floor(float64(t-n.size)/float64(n.slide))) - 1
	hi := int64(math.Ceil(float64(t)/float64(n.slide))) + 1
	for k := lo; k <= hi; k++ {
		s := k * n.slide
		if s <= t && s > t-n.size { // [s, s+size) 包含 t
			n.ensureStart(s)
			n.counts[s][key]++
		}
	}
}

func TestConcurrentWritesMatchNaiveReference(t *testing.T) {
	size, slide := 10*sec, 5*sec
	c, _ := New(Config{WindowSize: size, Slide: slide, MaxOpenWindows: 64})
	nv := newNaive(size, slide)

	type ev struct {
		ts  int64
		key string
	}
	var events []ev
	keys := []string{"k0", "k1", "k2", "k3", "k4"}
	for i := int64(0); i < 200; i++ {
		for _, k := range keys {
			events = append(events, ev{ts: i % 120, key: k})
		}
	}

	// 并发写入（事件顺序无关，因为计数加法可交换）。
	const workers = 8
	var wg sync.WaitGroup
	ch := make(chan ev)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for e := range ch {
				if err := c.Record(secAt(e.ts), e.key); err != nil {
					t.Errorf("unexpected record error: %v", err)
					return
				}
			}
		}()
	}
	for _, e := range events {
		nv.record(secAt(e.ts), e.key) // 朴素参照：顺序累计
		ch <- e                       // 同一事件交给计数器（由工作协程并发处理）
	}
	close(ch)
	wg.Wait()

	// 关闭全部窗口，得到实现的最终结果。
	got, err := c.Advance(secAt(1000))
	if err != nil {
		t.Fatal(err)
	}

	// 朴素参照的期望结果：按起点（终点）、键排序。
	sort.Slice(nv.starts, func(i, j int) bool { return nv.starts[i] < nv.starts[j] })
	var want []WindowResult
	for _, s := range nv.starts {
		var ks []string
		for k := range nv.counts[s] {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		for _, k := range ks {
			want = append(want, WindowResult{
				WindowStart: time.Unix(0, s),
				WindowEnd:   time.Unix(0, s+nv.size),
				Key:         k,
				Count:       nv.counts[s][k],
			})
		}
	}
	if !resultsEqual(got, want) {
		t.Fatalf("concurrent result diverges from naive reference:\n got %d rows\nwant %d rows",
			len(got), len(want))
	}
}

func TestConcurrentReadsAreConsistent(t *testing.T) {
	c, _ := New(Config{WindowSize: 10 * sec, Slide: 5 * sec, MaxOpenWindows: 64})

	var stop sync.WaitGroup
	var writers sync.WaitGroup
	ready := make(chan struct{})

	// 写入者持续记录与推进（推进到极大值后重建时钟场景：只用不回退的前进序列）。
	for w := 0; w < 4; w++ {
		writers.Add(1)
		go func(w int) {
			defer writers.Done()
			<-ready
			for i := 0; i < 200; i++ {
				_ = c.Record(secAt(int64(w*7+i*3)%100), fmt.Sprintf("k%d", w))
			}
		}(w)
	}
	for r := 0; r < 4; r++ {
		stop.Add(1)
		go func() {
			defer stop.Done()
			<-ready
			for i := 0; i < 500; i++ {
				snap := c.Snapshot()
				// 逐字段一致性检查：所有行计数必须为正；窗口区间合法；
				// 起点按升序、每个窗口内按键升序。
				var prevStart int64 = math.MinInt64
				prevKey := ""
				for _, wc := range snap.WindowCounts {
					if wc.Count <= 0 {
						panic(fmt.Sprintf("non-positive count in snapshot: %+v", wc))
					}
					if !wc.WindowEnd.After(wc.WindowStart) {
						panic(fmt.Sprintf("invalid window interval: %+v", wc))
					}
					s := wc.WindowStart.UnixNano()
					if s < prevStart || (s == prevStart && wc.Key <= prevKey) {
						panic(fmt.Sprintf("snapshot not sorted: %+v", wc))
					}
					prevStart, prevKey = s, wc.Key
				}
				if snap.Dropped < 0 {
					panic("negative dropped count")
				}
			}
		}()
	}
	close(ready)
	writers.Wait()
	stop.Wait() // 写入结束后读者很快自然结束；若仍在跑也不影响，等待其完成
}

func TestDeterministicRepeatedRuns(t *testing.T) {
	script := func(c *Counter) []WindowResult {
		var all []WindowResult
		steps := []struct {
			op   string
			ts   int64
			keys []string
		}{
			{"rec", -3, []string{"a", "b"}},
			{"adv", 0, nil},
			{"rec", -2, []string{"a"}}, // 部分迟到
			{"rec", 7, []string{"a", "c"}},
			{"adv", 5, nil},
			{"rec", 4, []string{"b"}},
			{"adv", 20, nil},
			{"rec", 9, []string{"z"}}, // 时钟已到 20，整条丢弃
		}
		for _, s := range steps {
			switch s.op {
			case "rec":
				for _, k := range s.keys {
					if err := c.Record(secAt(s.ts), k); err != nil {
						// 丢弃场景不报错；其它错误也不应出现
						t.Fatalf("unexpected error at %+v: %v", s, err)
					}
				}
			case "adv":
				out, err := c.Advance(secAt(s.ts))
				if err != nil {
					t.Fatal(err)
				}
				all = append(all, out...)
			}
		}
		return all
	}

	var first []WindowResult
	for run := 0; run < 3; run++ {
		c, _ := New(Config{WindowSize: 10 * sec, Slide: 5 * sec, MaxOpenWindows: 8})
		got := script(c)
		if run == 0 {
			first = got
			continue
		}
		if !resultsEqual(got, first) {
			// 该脚本不含并发，主要验证输出顺序稳定。
			t.Fatalf("run %d not deterministic:\nfirst=%v\ngot  =%v", run, first, got)
		}
		if c.Snapshot().Dropped != 1 {
			t.Fatalf("run %d: expected exactly 1 dropped event", run)
		}
	}
}

// ---------------------------------------------------------------------------
// 日志：输入、输出与判定依据
// ---------------------------------------------------------------------------

func TestLoggerPrintsInputsOutputsAndDecisions(t *testing.T) {
	var buf bytes.Buffer
	logger := &bufLogger{buf: &buf}
	c, _ := New(Config{WindowSize: 10 * sec, Slide: 5 * sec, MaxOpenWindows: 4, Logger: logger})

	_ = c.Record(secAt(0), "a")
	_ = c.Record(secAt(0), "")        // 拒绝：空键
	_, _ = c.Advance(secAt(10))       // 关闭并输出
	_ = c.Record(secAt(-100), "late") // 整条丢弃
	_, _ = c.Advance(secAt(9))        // 时钟回退拒绝
	log := buf.String()
	t.Logf("sample log:\n%s", log)

	for _, want := range []string{
		"RECORD",     // 输入
		"ADVANCE",    // 输入
		"COUNT into", // 计入依据
		"REJECT empty key",
		"DROP", // 丢弃依据
		"REJECT clock regression",
		"emitted", // 输出
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log missing %q\n--- log ---\n%s", want, log)
		}
	}
}

type bufLogger struct{ buf *bytes.Buffer }

func (l *bufLogger) Printf(format string, args ...any) {
	l.buf.WriteString(fmt.Sprintf(format+"\n", args...))
}

// ---------------------------------------------------------------------------
// 辅助比较
// ---------------------------------------------------------------------------

func resultsEqual(a, b []WindowResult) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !a[i].WindowStart.Equal(b[i].WindowStart) ||
			!a[i].WindowEnd.Equal(b[i].WindowEnd) ||
			a[i].Key != b[i].Key ||
			a[i].Count != b[i].Count {
			return false
		}
	}
	return true
}

func snapshotsEqual(a, b StateSnapshot) bool {
	if !a.Clock.Equal(b.Clock) || a.Dropped != b.Dropped || len(a.WindowCounts) != len(b.WindowCounts) {
		return false
	}
	for i := range a.WindowCounts {
		x, y := a.WindowCounts[i], b.WindowCounts[i]
		if !x.WindowStart.Equal(y.WindowStart) || !x.WindowEnd.Equal(y.WindowEnd) ||
			x.Key != y.Key || x.Count != y.Count {
			return false
		}
	}
	return true
}

func errorIs(err, target error) bool { return errors.Is(err, target) }

func asError(err error, target **Error) bool { return errors.As(err, target) }
