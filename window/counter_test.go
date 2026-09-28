package window

import (
	"errors"
	"log/slog"
	"math"
	"reflect"
	"sync"
	"testing"
)

// testWriter 把结构化日志写入测试输出（go test -v 时可见）。
type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Logf("%s", p)
	return len(p), nil
}

func newTestLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(testWriter{t: t}, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
}

func mustNew(t *testing.T, cfg Config) *Counter {
	t.Helper()
	if cfg.Logger == nil {
		cfg.Logger = newTestLogger(t)
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New() unexpected error: %v", err)
	}
	return c
}

func TestNewValidation(t *testing.T) {
	base := Config{WindowSize: 10, WatermarkDelay: 1, AllowedLateness: 1, MaxOpenWindows: 4}

	cases := []struct {
		name string
		mod  func(*Config)
		want error
	}{
		{"zero window size", func(c *Config) { c.WindowSize = 0 }, ErrInvalidWindowSize},
		{"negative window size", func(c *Config) { c.WindowSize = -5 }, ErrInvalidWindowSize},
		{"negative delay", func(c *Config) { c.WatermarkDelay = -1 }, ErrInvalidDelay},
		{"negative lateness", func(c *Config) { c.AllowedLateness = -1 }, ErrInvalidLateness},
		{"zero window limit", func(c *Config) { c.MaxOpenWindows = 0 }, ErrInvalidWindowLimit},
		{"negative window limit", func(c *Config) { c.MaxOpenWindows = -3 }, ErrInvalidWindowLimit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			tc.mod(&cfg)
			c, err := New(cfg)
			if !errors.Is(err, tc.want) {
				t.Fatalf("New() error = %v, want %v", err, tc.want)
			}
			if c != nil {
				t.Fatalf("New() returned counter on invalid config")
			}
		})
	}

	t.Run("valid", func(t *testing.T) {
		if c := mustNew(t, base); c == nil {
			t.Fatal("New() returned nil counter for valid config")
		}
	})
}

func TestWindowAssignment(t *testing.T) {
	// 极大延迟保证水位线永远不会触发任何窗口。
	c := mustNew(t, Config{
		WindowSize: 10, WatermarkDelay: 1 << 60, MaxOpenWindows: 100,
	})

	// 事件时间：-11 属于 [-20,-10)；-10/-1 属于 [-10,0)；0/9 属于 [0,10)；10 属于 [10,20)。
	events := []int64{-11, -10, -1, 0, 9, 10}
	for _, et := range events {
		if _, err := c.Add("k", et); err != nil {
			t.Fatalf("Add(k,%d) error: %v", et, err)
		}
	}

	got := c.ActiveWindows()
	want := []WindowState{
		{Key: "k", WindowStart: -20, WindowEnd: -10, Count: 1},
		{Key: "k", WindowStart: -10, WindowEnd: 0, Count: 2},
		{Key: "k", WindowStart: 0, WindowEnd: 10, Count: 2},
		{Key: "k", WindowStart: 10, WindowEnd: 20, Count: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ActiveWindows() = %+v, want %+v", got, want)
	}
}

func TestWatermarkMonotonic(t *testing.T) {
	c := mustNew(t, Config{WindowSize: 10, WatermarkDelay: 3, MaxOpenWindows: 10})

	if wm := c.Watermark(); wm != math.MinInt64 {
		t.Fatalf("initial watermark = %d, want MinInt64", wm)
	}
	if _, ok := c.MaxEventTime(); ok {
		t.Fatal("initial MaxEventTime should report not-set")
	}

	c.Add("k", -1) // wm = -1-3 = -4
	if wm := c.Watermark(); wm != -4 {
		t.Fatalf("watermark = %d, want -4", wm)
	}
	c.Add("k", -5) // 更旧的事件不回退水位线
	if wm := c.Watermark(); wm != -4 {
		t.Fatalf("watermark regressed to %d", wm)
	}
	c.Add("k", 10) // wm = 7，触发 [-10,0)
	if wm := c.Watermark(); wm != 7 {
		t.Fatalf("watermark = %d, want 7", wm)
	}
	res := c.Results()
	if len(res) != 1 || res[0].WindowStart != -10 || res[0].Count != 2 {
		t.Fatalf("Results() = %+v, want single fired window [-10,0) count 2", res)
	}
}

func TestWatermarkTriggerAndClear(t *testing.T) {
	c := mustNew(t, Config{
		WindowSize: 10, WatermarkDelay: 0, AllowedLateness: 0, MaxOpenWindows: 10,
	})

	c.Add("a", 1)
	c.Add("a", 9)
	c.Add("a", 10) // wm=10：[0,10) 到期，count=2

	res := c.Results()
	if len(res) != 1 {
		t.Fatalf("results len = %d, want 1", len(res))
	}
	r := res[0]
	if r.Kind != ResultTriggered || r.Count != 2 || r.WindowStart != 0 || r.WindowEnd != 10 || r.Seq != 1 {
		t.Fatalf("unexpected first result: %+v", r)
	}
	// wm == end 边界：触发但尚未清除（清除要求严格越过）。
	states := c.ActiveWindows()
	if len(states) != 2 || !states[0].Fired || states[0].WindowStart != 0 {
		t.Fatalf("after trigger states = %+v, want fired [0,10) + open [10,20)", states)
	}

	c.Add("a", 20) // wm=20：触发 [10,20) count=1；清除 [0,10)
	res = c.Results()
	if len(res) != 2 || res[1].Count != 1 || res[1].WindowStart != 10 {
		t.Fatalf("results = %+v, want second fire [10,20) count 1", res)
	}
	states = c.ActiveWindows()
	// [0,10) 已清除；[10,20) 已触发待清除；[20,30) 开窗（事件 20 属于它）。
	if len(states) != 2 || states[0].WindowStart != 10 || !states[0].Fired ||
		states[1].WindowStart != 20 || states[1].Fired {
		t.Fatalf("after clear states = %+v, want fired [10,20) + open [20,30)", states)
	}

	// 推进水位线清除 [10,20)（lateness=0，wm 严格越过 20）。
	c.Add("a", 21)
	if states = c.ActiveWindows(); len(states) != 1 || states[0].WindowStart != 20 {
		t.Fatalf("states = %+v, want only [20,30)", states)
	}

	// 已清除窗口再来迟到事件：丢弃，且不重建。
	out, err := c.Add("a", 5)
	if err != nil || out != OutcomeDropped {
		t.Fatalf("late Add() = (%v,%v), want (Dropped,nil)", out, err)
	}
	if c.DroppedEvents() != 1 {
		t.Fatalf("dropped = %d, want 1", c.DroppedEvents())
	}
	if states = c.ActiveWindows(); len(states) != 1 {
		t.Fatalf("dropped late event resurrected window: %+v", states)
	}
}

func TestLatenessBoundary(t *testing.T) {
	c := mustNew(t, Config{
		WindowSize: 10, WatermarkDelay: 0, AllowedLateness: 5, MaxOpenWindows: 10,
	})

	c.Add("a", 0)
	c.Add("a", 9)
	c.Add("a", 10) // wm=10：触发 [0,10) count=2，gcEnd=15，保留待修正

	// wm=10 <= gcEnd=15：容限内迟到，修正 count=3。
	if out, err := c.Add("a", 5); err != nil || out != OutcomeAccepted {
		t.Fatalf("within-lateness Add = (%v,%v)", out, err)
	}
	res := c.Results()
	if len(res) != 2 || res[1].Kind != ResultCorrected || res[1].Count != 3 || res[1].Seq != 2 {
		t.Fatalf("correction result wrong: %+v", res)
	}

	// 水位线恰好等于 gcEnd=15：边界仍算容限内，接受并修正 count=4。
	c.Add("a", 15) // 进入 [10,20)，wm=15
	c.Add("a", 4)  // 迟到修正 [0,10)
	res = c.Results()
	if len(res) != 3 || res[2].Kind != ResultCorrected || res[2].Count != 4 {
		t.Fatalf("boundary correction wrong: %+v", res)
	}
	if states := c.ActiveWindows(); len(states) != 2 {
		t.Fatalf("states at boundary = %+v, window must be retained at wm==gcEnd", states)
	}

	// wm=16 严格越过 gcEnd=15：[0,10) 被清除，再来事件丢弃。
	c.Add("a", 16)
	if states := c.ActiveWindows(); len(states) != 1 || states[0].WindowStart != 10 {
		t.Fatalf("states after gc = %+v, want only [10,20)", states)
	}
	if out, _ := c.Add("a", 3); out != OutcomeDropped {
		t.Fatalf("beyond-lateness Add outcome = %v, want Dropped", out)
	}
	if c.DroppedEvents() != 1 {
		t.Fatalf("dropped = %d, want 1", c.DroppedEvents())
	}
}

func TestRejectionLeavesStateUntouched(t *testing.T) {
	t.Run("empty key", func(t *testing.T) {
		c := mustNew(t, Config{WindowSize: 10, MaxOpenWindows: 10})
		c.Add("a", 1)
		wmBefore := c.Watermark()
		dropBefore := c.DroppedEvents()
		resBefore := len(c.Results())

		out, err := c.Add("", 999)
		if !errors.Is(err, ErrEmptyKey) || out != OutcomeRejected {
			t.Fatalf("Add(\"\") = (%v,%v), want (Rejected,ErrEmptyKey)", out, err)
		}
		if c.Watermark() != wmBefore || c.DroppedEvents() != dropBefore ||
			len(c.Results()) != resBefore {
			t.Fatal("empty-key rejection changed state")
		}
		if et, ok := c.MaxEventTime(); !ok || et != 1 {
			t.Fatalf("MaxEventTime = (%d,%v), want (1,true)", et, ok)
		}
		// 之后仍可正常使用。
		if _, err := c.Add("b", 2); err != nil {
			t.Fatalf("counter unusable after rejection: %v", err)
		}
	})

	t.Run("too many open windows", func(t *testing.T) {
		// 延迟足够大，任何窗口都不会触发或清除。
		c := mustNew(t, Config{
			WindowSize: 10, WatermarkDelay: 1_000_000, MaxOpenWindows: 2,
		})
		c.Add("k1", 0)
		c.Add("k2", 0)

		out, err := c.Add("k3", 500)
		if !errors.Is(err, ErrTooManyOpenWindows) || out != OutcomeRejected {
			t.Fatalf("over-limit Add = (%v,%v), want (Rejected,ErrTooManyOpenWindows)", out, err)
		}
		if states := c.ActiveWindows(); len(states) != 2 {
			t.Fatalf("open windows = %d after rejection, want 2", len(states))
		}
		if et, ok := c.MaxEventTime(); !ok || et != 0 {
			t.Fatalf("MaxEventTime = (%d,%v), rejection must not advance it", et, ok)
		}
		if c.DroppedEvents() != 0 || len(c.Results()) != 0 {
			t.Fatal("rejection changed dropped count or outputs")
		}
		// 已存在窗口仍可继续接收事件；拒绝不影响后续使用。
		if _, err := c.Add("k1", 5); err != nil {
			t.Fatalf("counter unusable after rejection: %v", err)
		}
		if states := c.ActiveWindows(); len(states) != 2 {
			t.Fatalf("open windows = %d, want 2", len(states))
		}
	})
}

type snapshot struct {
	wm      int64
	maxET   int64
	maxSet  bool
	dropped int64
	results []Result
	windows []WindowState
}

func takeSnapshot(c *Counter) snapshot {
	maxET, ok := c.MaxEventTime()
	return snapshot{
		wm:      c.Watermark(),
		maxET:   maxET,
		maxSet:  ok,
		dropped: c.DroppedEvents(),
		results: c.Results(),
		windows: c.ActiveWindows(),
	}
}

func replay(t *testing.T) snapshot {
	t.Helper()
	c := mustNew(t, Config{
		WindowSize: 10, WatermarkDelay: 2, AllowedLateness: 4, MaxOpenWindows: 50,
	})
	// 固定序列：乱序、负时间、多键、触发、容限内修正、超容限丢弃。
	seq := []struct {
		k string
		t int64
	}{
		{"a", -3}, {"a", -1}, {"b", 0}, {"a", 10}, {"b", 9},
		{"a", -2}, {"b", 10}, {"a", 5}, {"b", 1}, {"a", 20},
		{"b", 30}, {"a", 9}, {"b", -1}, {"a", 0},
	}
	for _, e := range seq {
		c.Add(e.k, e.t)
	}
	return takeSnapshot(c)
}

func TestDeterminism(t *testing.T) {
	first := replay(t)
	for i := 0; i < 3; i++ {
		got := replay(t)
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("run %d differs:\n got  %+v\n want %+v", i+2, got, first)
		}
	}
}

func TestConcurrentReads(t *testing.T) {
	c := mustNew(t, Config{
		WindowSize: 10, WatermarkDelay: 1, AllowedLateness: 3, MaxOpenWindows: 100,
	})

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// 并发只读：在写入进行期间持续读取，要求每次读取都能拿到自洽的快照。
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					s := takeSnapshot(c)
					// 自洽性：序号单调、结果数与最大序号一致。
					var lastSeq int64
					for _, r := range s.results {
						if r.Seq <= lastSeq {
							t.Errorf("non-monotonic seq in snapshot: %+v", r)
							return
						}
						lastSeq = r.Seq
					}
					if int64(len(s.results)) != lastSeq {
						t.Errorf("results len %d != last seq %d", len(s.results), lastSeq)
						return
					}
				}
			}
		}()
	}

	keys := []string{"a", "b", "c"}
	for et := int64(-25); et < 200; et++ {
		idx := et % 3
		if idx < 0 {
			idx += 3
		}
		if _, err := c.Add(keys[idx], et); err != nil {
			t.Fatalf("Add error: %v", err)
		}
	}
	close(stop)
	wg.Wait()

	// 补一个明确超容限的迟到事件，覆盖 dropped 计数路径。
	if out, err := c.Add("a", -100); err != nil || out != OutcomeDropped {
		t.Fatalf("late Add = (%v,%v), want (Dropped,nil)", out, err)
	}

	// 写入结束后，多个并发只读快照必须逐字段完全一致。
	final := make([]snapshot, 6)
	var wg2 sync.WaitGroup
	for i := range final {
		wg2.Add(1)
		go func(i int) {
			defer wg2.Done()
			final[i] = takeSnapshot(c)
		}(i)
	}
	wg2.Wait()
	for i := 1; i < len(final); i++ {
		if !reflect.DeepEqual(final[i], final[0]) {
			t.Fatalf("final snapshot %d differs:\n %+v\n vs %+v", i, final[i], final[0])
		}
	}
	if final[0].dropped == 0 || len(final[0].results) == 0 {
		t.Fatalf("expected non-trivial final state: %+v", final[0])
	}
}
