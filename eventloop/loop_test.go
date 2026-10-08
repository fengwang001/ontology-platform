package eventloop

import (
	"reflect"
	"sync"
	"testing"
)

func testConfig() Config {
	return Config{
		StarvationLimit:       4,
		FrameInterval:         10,
		MinTimerDelay:         1,
		TimerNestingThreshold: 2,
		TimerClampDelay:       100,
	}
}

func newLoop(t *testing.T, cfg Config) *EventLoop {
	t.Helper()
	l, err := NewEventLoop(cfg)
	if err != nil {
		t.Fatalf("NewEventLoop: %v", err)
	}
	return l
}

func advance(t *testing.T, l *EventLoop, target int64) AdvanceResult {
	t.Helper()
	res, err := l.AdvanceTo(target)
	if err != nil {
		t.Fatalf("AdvanceTo(%d): %v", target, err)
	}
	t.Logf("advance [%d -> %d]:", res.From, res.To)
	for _, e := range res.Entries {
		t.Logf("  %s", e)
	}
	for _, r := range res.Errors {
		t.Logf("  error t=%d h=%d: %v", r.Time, r.Handle, r.Value)
	}
	return res
}

// taskOrder 过滤出轨迹中的任务执行（按句柄）。
func taskOrder(entries []TraceEntry) []Handle {
	var out []Handle
	for _, e := range entries {
		if e.Type == ExecTask {
			out = append(out, e.Handle)
		}
	}
	return out
}

func TestMicrotaskDeepNesting(t *testing.T) {
	l := newLoop(t, testConfig())
	var order []string
	l.EnqueueTask(SourceNetwork, func() {
		order = append(order, "task1")
		l.QueueMicrotask(func() {
			order = append(order, "m1")
			l.QueueMicrotask(func() {
				order = append(order, "m2")
				l.QueueMicrotask(func() {
					order = append(order, "m3")
					// 微任务中入队的任务不得在检查点结束前执行。
					l.EnqueueTask(SourceNetwork, func() { order = append(order, "task3") })
				})
			})
		})
	})
	l.EnqueueTask(SourceNetwork, func() { order = append(order, "task2") })

	advance(t, l, 0)
	want := []string{"task1", "m1", "m2", "m3", "task2", "task3"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

func TestStarvationLimitExactlyReached(t *testing.T) {
	cfg := testConfig()
	cfg.StarvationLimit = 2
	l := newLoop(t, cfg)
	a, _ := l.EnqueueTask(SourceNetwork, func() {})
	u1, _ := l.EnqueueTask(SourceUserInteraction, func() {})
	u2, _ := l.EnqueueTask(SourceUserInteraction, func() {})
	u3, _ := l.EnqueueTask(SourceUserInteraction, func() {})

	res := advance(t, l, 0)
	// 网络源队首被跳过 2 轮达到上限，下一次选择必须被选中。
	want := []Handle{u1, u2, a, u3}
	if got := taskOrder(res.Entries); !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestFrameBoundaryEqualsTaskEnd(t *testing.T) {
	l := newLoop(t, testConfig()) // FrameInterval = 10
	l.SetTimeout(func() {}, 10)   // 到期时刻恰为帧边界 10
	raf, _ := l.RequestAnimationFrame(func() {})

	res := advance(t, l, 10)
	// 任务在 t=10 结束，渲染紧随其微任务之后插入。
	if len(res.Entries) != 2 {
		t.Fatalf("entries = %v, want 2", res.Entries)
	}
	if res.Entries[0].Type != ExecTask || res.Entries[0].Source != SourceTimer || res.Entries[0].Time != 10 {
		t.Fatalf("entries[0] = %v, want timer task @10", res.Entries[0])
	}
	if res.Entries[1].Type != ExecAnimationFrame || res.Entries[1].Handle != raf || res.Entries[1].Time != 10 {
		t.Fatalf("entries[1] = %v, want raf @10", res.Entries[1])
	}
}

func TestMissedFrameBoundariesCollapse(t *testing.T) {
	l := newLoop(t, testConfig())
	count := 0
	l.RequestAnimationFrame(func() { count++ })

	res := advance(t, l, 55) // 连续错过边界 10..50，只补一次渲染
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
	if len(res.Entries) != 1 || res.Entries[0].Time != 55 {
		t.Fatalf("entries = %v, want single raf @55", res.Entries)
	}

	l.RequestAnimationFrame(func() { count++ })
	res = advance(t, l, 105)
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
	if len(res.Entries) != 1 || res.Entries[0].Time != 105 {
		t.Fatalf("entries = %v, want single raf @105", res.Entries)
	}
}

func TestSkippedBoundariesNotCountedAsMissed(t *testing.T) {
	l := newLoop(t, testConfig())
	advance(t, l, 100) // 无渲染请求，边界 10..100 被跳过且不计为错过

	count := 0
	l.RequestAnimationFrame(func() { count++ }) // 注册于 t=100
	res := advance(t, l, 109)
	if count != 0 || len(res.Entries) != 0 {
		t.Fatalf("raf ran before next boundary: count=%d entries=%v", count, res.Entries)
	}
	res = advance(t, l, 110)
	if count != 1 || len(res.Entries) != 1 || res.Entries[0].Time != 110 {
		t.Fatalf("entries = %v, want single raf @110", res.Entries)
	}
}

func TestRenderDeferredRegistrationAndDirtyClear(t *testing.T) {
	l := newLoop(t, testConfig())
	var order []string
	l.RequestAnimationFrame(func() {
		order = append(order, "raf1")
		l.RequestAnimationFrame(func() { order = append(order, "raf3") }) // 推迟到下一次渲染
		l.QueueMicrotask(func() { order = append(order, "m-after-raf1") })
	})
	l.RequestAnimationFrame(func() { order = append(order, "raf2") })
	l.MarkDirty()

	advance(t, l, 10)
	want := []string{"raf1", "m-after-raf1", "raf2"} // 每个回调后都有微任务检查点
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}

	res := advance(t, l, 20) // 脏标记已清除；只有推迟注册的 raf3
	if len(res.Entries) != 1 || res.Entries[0].Type != ExecAnimationFrame {
		t.Fatalf("entries = %v, want single raf", res.Entries)
	}
	if !reflect.DeepEqual(order, append(want, "raf3")) {
		t.Fatalf("order = %v", order)
	}
}

func TestIdleZeroRemaining(t *testing.T) {
	l := newLoop(t, testConfig()) // FrameInterval = 10
	var got []IdleDeadline
	l.RequestIdleCallback(func(d IdleDeadline) { got = append(got, d) }, NoTimeout)

	advance(t, l, 10) // t=0 与 t=10 剩余时间恰为零，不得执行
	if len(got) != 0 {
		t.Fatalf("idle ran with zero remaining: %v", got)
	}
	advance(t, l, 15)
	if len(got) != 1 || got[0].Remaining != 5 || got[0].Deadline != 20 || got[0].DidTimeout {
		t.Fatalf("got = %v, want remaining=5 deadline=20", got)
	}
}

func TestIdleTimeoutFiresAsInternalTask(t *testing.T) {
	l := newLoop(t, testConfig())
	var got []IdleDeadline
	l.RequestIdleCallback(func(d IdleDeadline) { got = append(got, d) }, 10)

	res := advance(t, l, 10)
	// t=0 与 t=10 剩余时间为零，空闲不执行；超时时刻到达转为内部源任务。
	if len(got) != 1 || !got[0].DidTimeout {
		t.Fatalf("got = %v, want one timeout execution", got)
	}
	if len(res.Entries) != 1 || res.Entries[0].Source != SourceInternal || res.Entries[0].Time != 10 {
		t.Fatalf("entries = %v, want internal task @10", res.Entries)
	}
	advance(t, l, 100) // 总共只执行一次
	if len(got) != 1 {
		t.Fatalf("got = %v, want exactly one execution", got)
	}
}

func TestIdleRunsBeforeTimeout(t *testing.T) {
	l := newLoop(t, testConfig())
	var got []IdleDeadline
	l.RequestIdleCallback(func(d IdleDeadline) { got = append(got, d) }, 100)

	advance(t, l, 3) // 剩余时间 7 > 0，正常空闲执行
	if len(got) != 1 || got[0].DidTimeout || got[0].Remaining != 7 {
		t.Fatalf("got = %v, want normal idle execution", got)
	}
	advance(t, l, 200) // 超时不再触发
	if len(got) != 1 {
		t.Fatalf("got = %v, want exactly one execution", got)
	}
}

func TestIdleTimeoutAndIdleCoincide(t *testing.T) {
	l := newLoop(t, testConfig())
	count := 0
	var timeout bool
	l.RequestIdleCallback(func(d IdleDeadline) {
		count++
		timeout = d.DidTimeout
	}, 5)
	l.MarkDirty() // 渲染请求阻塞空闲期，直到 t=10 的帧边界

	advance(t, l, 5)
	// 超时时刻与空闲条件同时成立：按超时路径确定性地执行一次。
	if count != 1 || !timeout {
		t.Fatalf("count=%d timeout=%v, want one timeout execution", count, timeout)
	}
	advance(t, l, 30)
	if count != 1 {
		t.Fatalf("count = %d, want exactly one execution", count)
	}
}

func TestIdleDeferredRegistration(t *testing.T) {
	l := newLoop(t, testConfig())
	var order []string
	l.RequestIdleCallback(func(d IdleDeadline) {
		order = append(order, "idle1")
		l.RequestIdleCallback(func(IdleDeadline) { order = append(order, "idle3") }, NoTimeout)
	}, NoTimeout)
	l.RequestIdleCallback(func(IdleDeadline) { order = append(order, "idle2") }, NoTimeout)

	advance(t, l, 5)
	// idle3 在同一空闲期内不得执行；时钟推进后的下一空闲期才执行。
	if !reflect.DeepEqual(order, []string{"idle1", "idle2"}) {
		t.Fatalf("order = %v", order)
	}
	advance(t, l, 7)
	if !reflect.DeepEqual(order, []string{"idle1", "idle2", "idle3"}) {
		t.Fatalf("order = %v", order)
	}
}

func TestStarvationOneRoundShort(t *testing.T) {
	cfg := testConfig()
	cfg.StarvationLimit = 3
	l := newLoop(t, cfg)
	a, _ := l.EnqueueTask(SourceNetwork, func() {})
	u1, _ := l.EnqueueTask(SourceUserInteraction, func() {})
	u2, _ := l.EnqueueTask(SourceUserInteraction, func() {})
	u3, _ := l.EnqueueTask(SourceUserInteraction, func() {})

	res := advance(t, l, 0)
	// 差一轮到达上限时不强制，用户交互继续优先；第 3 轮后才强制。
	want := []Handle{u1, u2, u3, a}
	if got := taskOrder(res.Entries); !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestStarvationTieBreakFixedSourceOrder(t *testing.T) {
	cfg := testConfig()
	cfg.StarvationLimit = 1
	l := newLoop(t, cfg)
	n, _ := l.EnqueueTask(SourceNetwork, func() {})
	m, _ := l.EnqueueTask(SourceMessage, func() {})
	u1, _ := l.EnqueueTask(SourceUserInteraction, func() {})
	u2, _ := l.EnqueueTask(SourceUserInteraction, func() {})

	res := advance(t, l, 0)
	// 第 1 轮 UI 优先；u2 与 m 同时到达上限且入队时刻并列，
	// 按固定源次序用户交互先于消息。
	want := []Handle{u1, n, u2, m}
	if got := taskOrder(res.Entries); !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestStarvationTieBreakByEnqueueTime(t *testing.T) {
	// 直接针对调度器：同时到达上限时取入队时刻最早者。
	var stats Stats
	s := scheduler{limit: 1, stats: &stats}
	mk := func(src Source, enq int64) *task {
		return &task{source: src, enqueueTime: enq}
	}
	n := mk(SourceNetwork, 5)
	m := mk(SourceMessage, 3) // 入队时刻更早
	u := mk(SourceUserInteraction, 0)
	s.enqueue(n)
	s.enqueue(m)
	s.enqueue(u)

	var order []*task
	for i := 0; i < 3; i++ {
		s.discardCancelledHeads()
		got, _ := s.selectNext()
		if got == nil {
			t.Fatalf("round %d: no task selected", i)
		}
		order = append(order, got)
	}
	// 第 1 轮 UI 优先；第 2 轮 network/message 同时到达上限，
	// message 入队时刻更早被选中。
	want := []*task{u, m, n}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

func TestTimerNestingThreshold(t *testing.T) {
	l := newLoop(t, testConfig()) // 阈值=2，钳制=100，最小=1
	l.SetTimeout(func() {         // 深度 0，due=1
		l.SetTimeout(func() { // 深度 1，due=2
			l.SetTimeout(func() { // 深度 2 == 阈值，不钳制，due=3
				l.SetTimeout(func() {}, 1) // 深度 3 > 阈值，钳制为 100，due=103
			}, 1)
		}, 1)
	}, 1)

	res := advance(t, l, 200)
	var times []int64
	for _, e := range res.Entries {
		if e.Type == ExecTask && e.Source == SourceTimer {
			times = append(times, e.Time)
		}
	}
	want := []int64{1, 2, 3, 103}
	if !reflect.DeepEqual(times, want) {
		t.Fatalf("times = %v, want %v", times, want)
	}
}

func TestTimerSameDueRegistrationOrder(t *testing.T) {
	l := newLoop(t, testConfig())
	var order []string
	l.SetTimeout(func() { order = append(order, "t1") }, 5)
	l.SetTimeout(func() { order = append(order, "t2") }, 5)
	l.SetTimeout(func() { order = append(order, "t3") }, 5)

	res := advance(t, l, 5)
	if !reflect.DeepEqual(order, []string{"t1", "t2", "t3"}) {
		t.Fatalf("order = %v", order)
	}
	// 定时器任务的入队时刻取到期时刻而非注册时刻。
	for _, e := range res.Entries {
		if e.Type == ExecTask && e.Time != 5 {
			t.Fatalf("timer task ran at %d, want 5", e.Time)
		}
	}
}

func TestTimerMinDelayClamp(t *testing.T) {
	l := newLoop(t, testConfig()) // MinTimerDelay = 1
	l.SetTimeout(func() {}, 0)    // 延迟 0 按最小值 1 处理
	res := advance(t, l, 10)
	if len(res.Entries) != 1 || res.Entries[0].Time != 1 {
		t.Fatalf("entries = %v, want timer @1", res.Entries)
	}
}

func TestCancelSkipsStarvation(t *testing.T) {
	cfg := testConfig()
	cfg.StarvationLimit = 1
	l := newLoop(t, cfg)
	a1, _ := l.EnqueueTask(SourceNetwork, func() { t.Error("cancelled task ran") })
	a2, _ := l.EnqueueTask(SourceNetwork, func() {})
	u1, _ := l.EnqueueTask(SourceUserInteraction, func() {})
	u2, _ := l.EnqueueTask(SourceUserInteraction, func() {})
	if err := l.Cancel(a1); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	res := advance(t, l, 0)
	// 被取消的 a1 不占饥饿轮次：a2 只被 u1 跳过 1 轮即达上限。
	want := []Handle{u1, a2, u2}
	if got := taskOrder(res.Entries); !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestCancelEnqueuedTimerTask(t *testing.T) {
	l := newLoop(t, testConfig())
	var victim Handle
	l.SetTimeout(func() { // 先注册，同一到期时刻先执行
		if err := l.Cancel(victim); err != nil {
			t.Errorf("Cancel: %v", err)
		}
	}, 5)
	victim, _ = l.SetTimeout(func() { t.Error("cancelled timer ran") }, 5)

	res := advance(t, l, 10)
	// 已入队但未执行的定时器任务被取消后绝不执行。
	if len(res.Entries) != 1 {
		t.Fatalf("entries = %v, want only the cancelling timer", res.Entries)
	}
}

func TestPanicDoesNotInterrupt(t *testing.T) {
	l := newLoop(t, testConfig())
	var order []string
	l.EnqueueTask(SourceNetwork, func() {
		l.QueueMicrotask(func() { panic("p1") })
		l.QueueMicrotask(func() { order = append(order, "m2") })
		panic("p0")
	})
	l.EnqueueTask(SourceNetwork, func() { order = append(order, "task2") })
	l.RequestAnimationFrame(func() { panic("p2") })
	l.RequestAnimationFrame(func() { order = append(order, "raf2") })

	advance(t, l, 30)
	want := []string{"m2", "task2", "raf2"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}

	reps := l.Errors()
	if len(reps) != 3 {
		t.Fatalf("reports = %v, want 3", reps)
	}
	// 报告次序等于抛出次序：p0(t=0) → p1(t=0) → p2(t=30)。
	gotVals := []any{reps[0].Value, reps[1].Value, reps[2].Value}
	if !reflect.DeepEqual(gotVals, []any{"p0", "p1", "p2"}) {
		t.Fatalf("report values = %v", gotVals)
	}
	if between := l.ErrorsBetween(0, 0); len(between) != 2 {
		t.Fatalf("ErrorsBetween(0,0) = %v, want 2", between)
	}
	if between := l.ErrorsBetween(1, 30); len(between) != 1 {
		t.Fatalf("ErrorsBetween(1,30) = %v, want 1", between)
	}
}

func TestErrorCategoriesAndRejection(t *testing.T) {
	if _, err := NewEventLoop(Config{FrameInterval: 0}); err == nil || err.(*Error).Category != ErrInvalidArg {
		t.Fatalf("non-positive frame interval: %v", err)
	}
	l := newLoop(t, testConfig())

	check := func(name string, err error, cat Category) {
		t.Helper()
		e, ok := err.(*Error)
		if !ok || e.Category != cat {
			t.Fatalf("%s: got %v, want category %s", name, err, cat)
		}
	}
	var err error
	if _, err = l.EnqueueTask(Source(9), func() {}); true {
		check("unknown source", err, ErrInvalidArg)
	}
	if _, err = l.EnqueueTask(SourceNetwork, nil); true {
		check("nil task cb", err, ErrInvalidArg)
	}
	if _, err = l.QueueMicrotask(nil); true {
		check("nil microtask", err, ErrInvalidArg)
	}
	if _, err = l.RequestAnimationFrame(nil); true {
		check("nil raf", err, ErrInvalidArg)
	}
	if _, err = l.RequestIdleCallback(nil, 1); true {
		check("nil idle", err, ErrInvalidArg)
	}
	if _, err = l.RequestIdleCallback(func(IdleDeadline) {}, -1); true {
		check("negative idle timeout", err, ErrInvalidArg)
	}
	if _, err = l.SetTimeout(nil, 1); true {
		check("nil timer cb", err, ErrInvalidArg)
	}
	if _, err = l.SetTimeout(func() {}, -5); true {
		check("negative delay", err, ErrInvalidArg)
	}
	check("unknown handle", l.Cancel(Handle(999)), ErrHandleNotFound)

	h, _ := l.EnqueueTask(SourceNetwork, func() {})
	if err := l.Cancel(h); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	check("duplicate cancel", l.Cancel(h), ErrDuplicateCancel)

	advance(t, l, 10)
	if _, err = l.AdvanceTo(5); true {
		check("clock rollback", err, ErrClockRollback)
	}
	// 被拒绝的操作不改变任何队列、标记与时钟。
	if l.Now() != 10 {
		t.Fatalf("clock changed after rejected advance: %d", l.Now())
	}
	if tr := l.Trace(); len(tr) != 0 {
		t.Fatalf("trace changed after rejected ops: %v", tr)
	}
}

func TestConcurrentEnqueuePreservesObservableOrder(t *testing.T) {
	l := newLoop(t, testConfig())
	record = nil
	const goroutines = 4
	const perG = 50
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				g, i := g, i
				l.EnqueueTask(SourceMessage, func() {
					recordMu.Lock()
					record = append(record, [2]int{g, i})
					recordMu.Unlock()
				})
			}
		}(g)
	}
	// 并发的标脏与取消烟雾测试（-race 下验证）。
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				l.MarkDirty()
				h, _ := l.RequestAnimationFrame(func() {})
				l.Cancel(h)
			}
		}
	}()
	wg.Wait()
	close(done)

	advance(t, l, 0)
	// 同一 goroutine 的入队先后关系在串行顺序中必须保持。
	next := make([]int, goroutines)
	for _, r := range record {
		g, i := r[0], r[1]
		if i != next[g] {
			t.Fatalf("goroutine %d: executed %d, want %d", g, i, next[g])
		}
		next[g]++
	}
	if len(record) != goroutines*perG {
		t.Fatalf("executed %d tasks, want %d", len(record), goroutines*perG)
	}
}

var (
	recordMu sync.Mutex
	record   [][2]int
)

func TestSelectionCostIndependentOfQueueSize(t *testing.T) {
	for _, n := range []int{100, 1000, 10000} {
		l := newLoop(t, testConfig())
		for i := 0; i < n; i++ {
			l.EnqueueTask(Source(i%numSources), func() {})
		}
		res := advance(t, l, 0)
		if len(res.Entries) != n {
			t.Fatalf("n=%d: entries = %d", n, len(res.Entries))
		}
		st := l.Stats()
		if st.Selections != int64(n) {
			t.Fatalf("n=%d: selections = %d", n, st.Selections)
		}
		// 每次选择只检查固定数量（<=2*源数）的队首，与队列总规模无关。
		if st.HeadChecks > 2*numSources*st.Selections {
			t.Fatalf("n=%d: head checks %d exceeds constant bound", n, st.HeadChecks)
		}
		// 微任务检查点次数等于任务执行次数，与队列规模无关。
		if st.Checkpoints != int64(n) {
			t.Fatalf("n=%d: checkpoints = %d, want %d", n, st.Checkpoints, n)
		}
	}
}
