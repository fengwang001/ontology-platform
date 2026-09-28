package hopping

import (
	"errors"
	"math"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type refEvent struct {
	ts    int64
	key   string
	count int64
}

// naiveReference 是独立于生产实现的朴素参照：直接暴力枚举窗口下标，
// 用最原始的 start <= ts < start+size 判定归属（不使用 floor/ceil 除法）。
// 每个事件重复 multiplier 次（模拟多个 goroutine 各加一遍）。
func naiveReference(events []refEvent, multiplier int, size, step int64) []WindowCount {
	windows := make(map[int64]map[string]int64)
	for _, e := range events {
		for k := int64(-200); k <= 200; k++ {
			start := k * step
			if start <= e.ts && e.ts < start+size {
				if windows[start] == nil {
					windows[start] = make(map[string]int64)
				}
				windows[start][e.key] += e.count * int64(multiplier)
			}
		}
	}
	var out []WindowCount
	for start, counts := range windows {
		for key, n := range counts {
			out = append(out, WindowCount{WindowStart: start, WindowEnd: start + size, Key: key, Count: n})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].WindowEnd != out[j].WindowEnd {
			return out[i].WindowEnd < out[j].WindowEnd
		}
		return out[i].Key < out[j].Key
	})
	return out
}

func makeEvents(n int) []refEvent {
	rng := rand.New(rand.NewSource(256))
	keys := []string{"alpha", "beta", "gamma", "delta"}
	ev := make([]refEvent, n)
	for i := range ev {
		ev[i] = refEvent{
			ts:    int64(rng.Intn(80)) - 30, // [-30, 50)
			key:   keys[rng.Intn(len(keys))],
			count: int64(1 + rng.Intn(3)),
		}
	}
	return ev
}

func TestConcurrentAddsMatchNaiveReference(t *testing.T) {
	const (
		size    = int64(10)
		step    = int64(5)
		writers = 8
		readers = 4
		nEvents = 500
		maxOpen = 100
	)
	c, err := New(Config{WindowSize: size, SlideStep: step, MaxOpenWindows: maxOpen})
	if err != nil {
		t.Fatal(err)
	}
	events := makeEvents(nEvents)

	var stopReaders atomic.Bool
	var readerWg sync.WaitGroup
	for r := 0; r < readers; r++ {
		readerWg.Add(1)
		go func() {
			defer readerWg.Done()
			var last Stats
			first := true
			for !stopReaders.Load() {
				st := c.Snapshot() // 逐字段一致快照：读到的是同一把锁下的值
				if st.OpenWindows < 0 || st.OpenWindows > maxOpen {
					t.Errorf("inconsistent OpenWindows=%d", st.OpenWindows)
					return
				}
				if st.Watermark != math.MinInt64 {
					t.Errorf("watermark moved before any Advance: %d", st.Watermark)
					return
				}
				if st.BufferedEvents < 0 || st.DroppedEvents != 0 ||
					st.EmittedWindows != 0 || st.EmittedRecords != 0 {
					t.Errorf("inconsistent snapshot during adds-only phase: %+v", st)
					return
				}
				if !first {
					// 只有写入在进行：所有单调量不得倒退。
					if st.BufferedEvents < last.BufferedEvents ||
						st.OpenWindows < last.OpenWindows {
						t.Errorf("counter moved backwards: %+v -> %+v", last, st)
						return
					}
				}
				last, first = st, false
			}
		}()
	}

	var writerWg sync.WaitGroup
	for w := 0; w < writers; w++ {
		writerWg.Add(1)
		go func() {
			defer writerWg.Done()
			for _, e := range events {
				if err := c.Add(e.ts, e.key, e.count); err != nil {
					t.Errorf("concurrent Add(%d,%q): %v", e.ts, e.key, err)
					return
				}
			}
		}()
	}
	writerWg.Wait()
	stopReaders.Store(true)
	readerWg.Wait()

	// 所有事件都在任何推进之前加入，没有任何窗口被关闭，因此丢弃必须为 0。
	if st := c.Snapshot(); st.DroppedEvents != 0 {
		t.Fatalf("DroppedEvents=%d, want 0", st.DroppedEvents)
	}

	got, err := c.Advance(math.MaxInt64)
	if err != nil {
		t.Fatal(err)
	}
	want := naiveReference(events, writers, size, step)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("concurrent result differs from naive reference:\ngot len=%d\nwant len=%d",
			len(got), len(want))
	}
}

func TestConcurrentMixedAddsAndAdvances(t *testing.T) {
	c, err := New(Config{WindowSize: 10, SlideStep: 5, MaxOpenWindows: 1000})
	if err != nil {
		t.Fatal(err)
	}

	var (
		mu       sync.Mutex
		emitted  []WindowCount
		addsOK   int64
		rejected int64
	)

	// 并发快照读取者：只做一致性校验，竞态由 -race 兜底。
	var stopReaders atomic.Bool
	var readerWg sync.WaitGroup
	for r := 0; r < 3; r++ {
		readerWg.Add(1)
		go func() {
			defer readerWg.Done()
			var last Stats
			first := true
			for !stopReaders.Load() {
				st := c.Snapshot()
				if st.OpenWindows < 0 || st.OpenWindows > 1000 {
					t.Errorf("inconsistent OpenWindows=%d", st.OpenWindows)
					return
				}
				if !first && (st.EmittedRecords < last.EmittedRecords ||
					st.EmittedWindows < last.EmittedWindows) {
					t.Errorf("emitted totals moved backwards: %+v -> %+v", last, st)
					return
				}
				last, first = st, false
			}
		}()
	}

	// 唯一的"合法推进者"，按固定递增水位推进；其他 goroutine 只做必定失败的回退尝试。
	firstAdvanceDone := make(chan struct{})
	var advancerWg sync.WaitGroup
	advancerWg.Add(1)
	go func() {
		defer advancerWg.Done()
		first, err := c.Advance(-100)
		if err != nil {
			t.Errorf("first advance: %v", err)
			return
		}
		mu.Lock()
		emitted = append(emitted, first...)
		mu.Unlock()
		close(firstAdvanceDone)
		for wm := int64(-90); wm <= 200; wm += 5 {
			out, err := c.Advance(wm)
			if err != nil {
				t.Errorf("Advance(%d): %v", wm, err)
				return
			}
			mu.Lock()
			emitted = append(emitted, out...)
			mu.Unlock()
			time.Sleep(time.Microsecond)
		}
	}()

	// 并发写入：时间戳跨越水位，天然制造部分迟到与整条丢弃。
	var addersWg sync.WaitGroup
	for w := 0; w < 6; w++ {
		addersWg.Add(1)
		go func(seed int64) {
			defer addersWg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 1000; i++ {
				ts := int64(rng.Intn(360)) - 120 // [-120,240)
				err := c.Add(ts, "k", 1)
				switch {
				case err == nil:
					atomic.AddInt64(&addsOK, 1)
				case errors.Is(err, ErrTooManyOpenWindows):
					atomic.AddInt64(&rejected, 1)
				default:
					t.Errorf("unexpected Add error: %v", err)
					return
				}
			}
			// 丢弃无法从返回值区分（返回 nil），由最终统计核对。
		}(int64(w + 1))
	}

	// 并发回退尝试：第一次合法推进之后，-101 永远低于当前水位。
	var rewindWg sync.WaitGroup
	<-firstAdvanceDone
	for w := 0; w < 2; w++ {
		rewindWg.Add(1)
		go func() {
			defer rewindWg.Done()
			for i := 0; i < 200; i++ {
				if _, err := c.Advance(-101); !errors.Is(err, ErrClockRewind) {
					t.Errorf("expected ErrClockRewind, got %v", err)
					return
				}
			}
		}()
	}

	addersWg.Wait()
	advancerWg.Wait()
	rewindWg.Wait()

	// 收尾：推进到最大，关闭所有残留窗口。
	final, err := c.Advance(math.MaxInt64)
	if err != nil {
		t.Fatal(err)
	}
	stopReaders.Store(true)
	readerWg.Wait()
	mu.Lock()
	emitted = append(emitted, final...)
	mu.Unlock()

	// 多次推进的拼接整体仍应按（终点, 键）有序：各批水位递增、批内有序。
	if !sort.SliceIsSorted(emitted, func(i, j int) bool {
		if emitted[i].WindowEnd != emitted[j].WindowEnd {
			return emitted[i].WindowEnd < emitted[j].WindowEnd
		}
		return emitted[i].Key < emitted[j].Key
	}) {
		t.Fatal("emitted records are not globally sorted by (end,key)")
	}

	// 不变量：(窗口, 键) 全局唯一——任何窗口只输出一次。
	type windowKey struct {
		start int64
		key   string
	}
	seen := make(map[windowKey]bool)
	var countSum int64
	for _, r := range emitted {
		k := windowKey{r.WindowStart, r.Key}
		if seen[k] {
			t.Fatalf("window/key emitted more than once: start=%d key=%q", r.WindowStart, r.Key)
		}
		seen[k] = true
		if r.Count <= 0 {
			t.Fatalf("non-positive count emitted: %+v", r)
		}
		countSum += r.Count
	}

	st := c.Snapshot()
	if st.OpenWindows != 0 || st.BufferedEvents != 0 {
		t.Fatalf("all windows should be closed, stats=%+v", st)
	}
	if st.Watermark != math.MaxInt64 {
		t.Fatalf("watermark=%d, want MaxInt64", st.Watermark)
	}
	if st.EmittedRecords != int64(len(emitted)) {
		t.Fatalf("EmittedRecords=%d, collected=%d", st.EmittedRecords, len(emitted))
	}
	if addsOK == 0 {
		t.Fatal("no accepted adds recorded")
	}
	// size/step=10/5 => 每个事件至多计入 2 个窗口；已输出计数总和不得超过此上界。
	if countSum > addsOK*2 {
		t.Fatalf("emitted count sum %d exceeds max possible %d", countSum, addsOK*2)
	}
	if st.DroppedEvents < 0 {
		t.Fatalf("negative dropped: %d", st.DroppedEvents)
	}
	t.Logf("accepted adds=%d dropped=%d capacity-rejected=%d emitted records=%d count sum=%d",
		addsOK, st.DroppedEvents, rejected, len(emitted), countSum)
}
