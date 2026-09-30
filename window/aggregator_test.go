package window

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// testLogger 把输入、输出与判定依据写入测试日志：go test -v 时可见。
type testLogger struct{ t *testing.T }

func (l testLogger) Printf(format string, args ...any) {
	l.t.Logf("%s", strings.TrimSpace(fmt.Sprintf(format, args...)))
}

func newAgg(t *testing.T, size, delay int64) (*Aggregator, *[]Result) {
	t.Helper()
	var out []Result
	var mu sync.Mutex
	a := New(size, delay, func(r Result) {
		mu.Lock()
		out = append(out, r)
		mu.Unlock()
	})
	a.SetLogger(testLogger{t})
	return a, &out
}

func ev(key string, t, v int64) Event { return Event{Key: key, Time: t, V: v} }

func assertResults(t *testing.T, got, want []Result) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d results %v, want %d results %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("result[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// assertInvariant 校验 总提交数 = 已输出条数 + 迟到数 + 在途窗口内条数。
// emittedCount 为 sink 已收到的结果总条数（窗口内事件数之和）。
func assertInvariant(t *testing.T, a *Aggregator, emittedCount int) {
	t.Helper()
	submitted, late, pending := a.Stats()
	if submitted != int64(emittedCount)+late+pending {
		t.Fatalf("invariant broken: submitted=%d, emitted=%d, late=%d, in-flight=%d",
			submitted, emittedCount, late, pending)
	}
}

// 场景一：4 -> 6，申请时 M 恰为 12，生效点 B=12。
func TestResize4to6_M_Exactly_12_Boundary_12(t *testing.T) {
	a, out := newAgg(t, 4, 1)

	a.Submit(ev("k", 9, 1)) // wm=8，持有窗口右端 12
	if got := a.Watermark(); got != 8 {
		t.Fatalf("watermark = %d, want 8", got)
	}

	b, err := a.Resize(6)
	if err != nil {
		t.Fatalf("resize: %v", err)
	}
	if b != 12 {
		t.Fatalf("B = %d, want 12 (lcm(4,6)=12, M=12)", b)
	}
	if ns, bb, ok := a.Pending(); !ok || ns != 6 || bb != 12 {
		t.Fatalf("pending = (%d,%d,%v), want (6,12,true)", ns, bb, ok)
	}

	// 升级期间：t=11 < B 走旧大小 4（右端 12）；t=13 >= B 走新大小 6（右端 18）。
	if !a.Submit(ev("k", 11, 10)) {
		t.Fatal("t=11 should be accepted under old size 4")
	}
	if !a.Submit(ev("k", 13, 100)) {
		t.Fatal("t=13 should be accepted under new size 6")
	}
	// t=13 使 wm=12 >= B=12：旧窗口右端 12 先输出，升级即时完成。
	if a.CurrentSize() != 6 {
		t.Fatalf("current size = %d, want 6 once watermark reached B", a.CurrentSize())
	}
	// t=17 仍归入新大小窗口 [12,18)；t=19 -> wm=18 时新窗口输出。
	a.Submit(ev("k", 17, 2))
	a.Submit(ev("k", 19, 1))

	want := []Result{
		{Key: "k", Start: 8, End: 12, Count: 2, Sum: 11},
		{Key: "k", Start: 12, End: 18, Count: 2, Sum: 102},
	}
	assertResults(t, *out, want)

	if _, _, ok := a.Pending(); ok {
		t.Fatal("no upgrade should remain pending after watermark >= B")
	}
	assertInvariant(t, a, resultEventCount(*out))
}

// 场景二：4 -> 6，申请时 M 略超 12（持有窗口右端 16），生效点 B=24。
func TestResize4to6_M_SlightlyAbove_12_Boundary_24(t *testing.T) {
	a, out := newAgg(t, 4, 1)

	a.Submit(ev("k", 13, 1)) // wm=12，持有窗口右端 16，M=16
	b, err := a.Resize(6)
	if err != nil {
		t.Fatalf("resize: %v", err)
	}
	if b != 24 {
		t.Fatalf("B = %d, want 24 (lcm=12, M=16)", b)
	}

	// 跨越 B 两侧：t=23 < 24 走旧大小 4（右端 24）；t=24 >= 24 走新大小 6（右端 30）。
	a.Submit(ev("k", 23, 10))
	a.Submit(ev("k", 24, 100))
	if a.CurrentSize() != 4 {
		t.Fatalf("size should stay 4 before watermark reaches B, got %d", a.CurrentSize())
	}

	// t=25 -> wm=24：旧窗口 [12,16)、[20,24) 先输出，随后升级完成。
	a.Submit(ev("k", 25, 7))
	want := []Result{
		{Key: "k", Start: 12, End: 16, Count: 1, Sum: 1},
		{Key: "k", Start: 20, End: 24, Count: 1, Sum: 10},
	}
	assertResults(t, *out, want)
	if a.CurrentSize() != 6 {
		t.Fatalf("current size = %d, want 6 after watermark reached B", a.CurrentSize())
	}

	// 新划分窗口 [24,30) 在水位达到 30 时输出。
	a.Submit(ev("k", 31, 2))
	want = append(want, Result{Key: "k", Start: 24, End: 30, Count: 2, Sum: 107})
	assertResults(t, *out, want)
	assertInvariant(t, a, resultEventCount(*out))
}

func resultEventCount(rs []Result) int {
	n := 0
	for _, r := range rs {
		n += int(r.Count)
	}
	return n
}

// 升级期间，迟到判定按两套划分分别进行。
func TestLateDuringPending_BothPartitions(t *testing.T) {
	a, out := newAgg(t, 4, 1)
	a.Submit(ev("a", 13, 1))               // wm=12，持有右端 16
	if _, err := a.Resize(6); err != nil { // B=24
		t.Fatalf("resize: %v", err)
	}
	a.Submit(ev("a", 17, 1)) // wm=16，右端 16 到期输出，持有右端 20

	// t=15 < B 走旧大小 4：右端 16 <= wm=16 -> 迟到。
	if a.Submit(ev("a", 15, 1)) {
		t.Fatal("t=15 must be late under old partition (end 16 <= wm 16)")
	}
	// t=17 < B 走旧大小 4：右端 20 > 16 -> 准时。
	if !a.Submit(ev("a", 17, 1)) {
		t.Fatal("t=17 must be on time under old partition (end 20 > wm 16)")
	}

	// 水位越过 B 升级完成；t=24 在新大小 6 下右端 30。
	// 同一事件按旧大小 4 右端为 28（在 wm=16 下会准时），以此证明判定按新划分。
	a.Submit(ev("a", 31, 1)) // wm=30，[24,30) 输出
	if a.Submit(ev("a", 24, 1)) {
		t.Fatal("t=24 must be late under new partition after upgrade (end 30 <= wm 30)")
	}
	if _, late, _ := a.Stats(); late != 2 {
		t.Fatalf("late count = %d, want 2", late)
	}
	assertInvariant(t, a, resultEventCount(*out))
}

// 升级前的基础迟到与负事件时间：水位初值为 0，任何右端 <= 0 的事件均迟到。
func TestLateBeforeUpgradeAndNegativeTime(t *testing.T) {
	a, out := newAgg(t, 4, 1)

	// 负事件时间：t=-1、t=-4 在大小 4 下窗口右端均为 0 <= wm=0 -> 迟到。
	if a.Submit(ev("k", -1, 9)) {
		t.Fatal("negative event t=-1 must be late (window end 0 <= watermark 0)")
	}
	if a.Submit(ev("k", -4, 9)) {
		t.Fatal("event t=-4 must be late (window end 0 <= watermark 0)")
	}
	if a.Watermark() != 0 {
		t.Fatalf("watermark must stay 0 after late events, got %d", a.Watermark())
	}

	// t=4 -> wm=3，窗口 [4,8)；t=3 右端 4 > 3，准时。
	a.Submit(ev("k", 4, 3))
	if !a.Submit(ev("k", 3, 1)) {
		t.Fatal("t=3 window end 4 > wm 3 should be on time")
	}
	// t=8 -> wm=7，两个窗口到期；随后 t=3 右端 4 <= 7 -> 迟到。
	a.Submit(ev("k", 8, 1))
	if a.Submit(ev("k", 3, 1)) {
		t.Fatal("t=3 must become late after watermark passed window end 4")
	}
	if _, late, _ := a.Stats(); late != 3 {
		t.Fatalf("late = %d, want 3", late)
	}
	assertInvariant(t, a, resultEventCount(*out))
}

// 非法升级输入：按序只报第一个可区分原因；拒绝不改变任何状态。
func TestResizeRejections(t *testing.T) {
	a, _ := newAgg(t, 4, 1)

	for _, bad := range []int64{0, -6} {
		if _, err := a.Resize(bad); !errors.Is(err, ErrNonPositiveSize) {
			t.Fatalf("resize(%d) err = %v, want ErrNonPositiveSize", bad, err)
		}
	}
	if _, err := a.Resize(4); !errors.Is(err, ErrSameSize) {
		t.Fatalf("resize(4) err = %v, want ErrSameSize", err)
	}

	// 建立待完成升级：t=5 -> wm=4，M=max(4,8)=8，B=12。
	a.Submit(ev("k", 5, 1))
	b0, err := a.Resize(6)
	if err != nil || b0 != 12 {
		t.Fatalf("resize(6) = (%d,%v), want (12,nil)", b0, err)
	}

	// 拒绝顺序固定：非正 -> 待完成 -> 相同 -> lcm，各只报第一个。
	if _, err := a.Resize(0); !errors.Is(err, ErrNonPositiveSize) {
		t.Fatalf("pending resize(0) err = %v, want ErrNonPositiveSize (checked first)", err)
	}
	if _, err := a.Resize(4); !errors.Is(err, ErrUpgradePending) {
		t.Fatalf("pending resize(4) err = %v, want ErrUpgradePending", err)
	}
	if _, err := a.Resize(6); !errors.Is(err, ErrUpgradePending) {
		t.Fatalf("pending resize(6) err = %v, want ErrUpgradePending", err)
	}

	// 拒绝不得改变水位、窗口状态与迟到计数。
	if a.Watermark() != 4 {
		t.Fatalf("watermark changed to %d, want 4", a.Watermark())
	}
	if ns, bb, ok := a.Pending(); !ok || ns != 6 || bb != 12 {
		t.Fatalf("pending state changed: (%d,%d,%v)", ns, bb, ok)
	}
	submitted, late, pending := a.Stats()
	if submitted != 1 || late != 0 || pending != 1 {
		t.Fatalf("stats after rejections = (%d,%d,%d), want (1,0,1)", submitted, late, pending)
	}

	// 完成升级后再测 lcm 超限：lcm(6,1e9) > 1e9。
	a.Submit(ev("k", 13, 1)) // wm=12，[8,12) 输出，升级完成
	if a.CurrentSize() != 6 {
		t.Fatalf("size = %d, want 6", a.CurrentSize())
	}
	if _, err := a.Resize(1_000_000_000); !errors.Is(err, ErrLCMLimitExceeded) {
		t.Fatalf("resize(1e9) err = %v, want ErrLCMLimitExceeded", err)
	}
	// 合法边界：lcm(6,2_000_000)=6_000_000 <= 1e9，应接受。
	if _, err := a.Resize(2_000_000); err != nil {
		t.Fatalf("resize(2000000) should be accepted (lcm=6000000), got %v", err)
	}
}

// 空闲时申请升级：M=0、B=0，水位立即满足，当场完成。
func TestResizeImmediateWhenIdle(t *testing.T) {
	a, out := newAgg(t, 4, 1)
	b, err := a.Resize(6)
	if err != nil {
		t.Fatalf("resize: %v", err)
	}
	if b != 0 {
		t.Fatalf("B = %d, want 0 (M=0 when idle)", b)
	}
	if a.CurrentSize() != 6 {
		t.Fatalf("size = %d, want 6 immediately", a.CurrentSize())
	}
	if _, _, ok := a.Pending(); ok {
		t.Fatal("upgrade should complete immediately when watermark >= B")
	}
	// t=7 -> [6,12)；t=13 -> [12,18)，wm=12 时前者先输出。
	a.Submit(ev("k", 7, 5))
	a.Submit(ev("k", 13, 2))
	assertResults(t, *out, []Result{{Key: "k", Start: 6, End: 12, Count: 1, Sum: 5}})
	assertInvariant(t, a, resultEventCount(*out))
}

// 输出严格按 (右端, 键) 升序；条数与和正确；到期窗口输出后被清除。
func TestEmissionOrderAndAggregation(t *testing.T) {
	var out []Result
	var mu sync.Mutex
	a := New(4, 100, func(r Result) {
		mu.Lock()
		out = append(out, r)
		mu.Unlock()
	})
	a.SetLogger(testLogger{t})

	a.Submit(ev("b", 0, 1))
	a.Submit(ev("a", 0, 2))
	a.Submit(ev("a", 1, 10))
	a.Submit(ev("a", 4, 5))
	// t=203 -> wm=103：右端 4 先于右端 8；同右端按键升序。
	a.Submit(ev("a", 203, 0))

	want := []Result{
		{Key: "a", Start: 0, End: 4, Count: 2, Sum: 12},
		{Key: "b", Start: 0, End: 4, Count: 1, Sum: 1},
		{Key: "a", Start: 4, End: 8, Count: 1, Sum: 5},
	}
	assertResults(t, out, want)

	// 已输出窗口被清除，后续迟到事件不会补写旧结果。
	if a.Submit(ev("a", 0, 1)) {
		t.Fatal("event into an already emitted window must be late, not appended")
	}
	if len(out) != len(want) {
		t.Fatal("emitted results must never be rewritten")
	}
	assertInvariant(t, a, resultEventCount(out))
}

// 并发调用：大量 Submit 与 Resize 并发执行，不变量恒成立；
// 同一操作序列按相同顺序串行重放，输出与统计完全一致（确定性）。
func TestConcurrentAndDeterministicReplay(t *testing.T) {
	run := func(serial bool) ([]Result, [3]int64) {
		var out []Result
		var mu sync.Mutex
		a := New(4, 2, func(r Result) {
			mu.Lock()
			out = append(out, r)
			mu.Unlock()
		})
		if serial {
			a.SetLogger(testLogger{t})
		}

		type op struct {
			resize bool
			e      Event
			s      int64
		}
		var ops []op
		for i := int64(0); i < 500; i++ {
			ops = append(ops, op{e: Event{
				Key:  fmt.Sprintf("k%d", i%7),
				Time: i,
				V:    i,
			}})
			if i%37 == 18 {
				ops = append(ops, op{resize: true, s: 6})
				ops = append(ops, op{resize: true, s: 4})
			}
		}

		play := func(idx int) {
			o := ops[idx]
			if o.resize {
				_, _ = a.Resize(o.s) // 可能因待完成/相同被拒绝，两种重放均一致
			} else {
				_ = a.Submit(o.e)
			}
		}

		if serial {
			for i := range ops {
				play(i)
			}
		} else {
			var wg sync.WaitGroup
			for i := range ops {
				wg.Add(1)
				go func(i int) { defer wg.Done(); play(i) }(i)
			}
			wg.Wait()
		}

		mu.Lock()
		defer mu.Unlock()
		submitted, late, pending := a.Stats()
		return append([]Result(nil), out...), [3]int64{submitted, late, pending}
	}

	// 两次串行重放必须逐字节一致。
	out1, st1 := run(true)
	out2, st2 := run(true)
	if st1 != st2 {
		t.Fatalf("non-deterministic stats: %v vs %v", st1, st2)
	}
	if len(out1) != len(out2) {
		t.Fatalf("non-deterministic output length: %d vs %d", len(out1), len(out2))
	}
	for i := range out1 {
		if out1[i] != out2[i] {
			t.Fatalf("output diverges at %d: %+v vs %+v", i, out1[i], out2[i])
		}
	}

	// 并发跑只校验不变量（并发交织顺序允许不同）：submitted = emitted + late + in-flight。
	outC, stC := run(false)
	emitted := int64(resultEventCount(outC))
	if stC[0] != emitted+stC[1]+stC[2] {
		t.Fatalf("concurrent invariant broken: submitted=%d emitted=%d late=%d inflight=%d",
			stC[0], emitted, stC[1], stC[2])
	}
	// 输出始终按 (右端, 键) 升序。
	for i := 1; i < len(outC); i++ {
		p, q := outC[i-1], outC[i]
		if p.End > q.End || p.End == q.End && p.Key > q.Key {
			t.Fatalf("output not sorted at %d: %+v before %+v", i, p, q)
		}
	}
}

func TestFloorAndCeilingHelpers(t *testing.T) {
	cases := []struct {
		t, s, end int64
	}{
		{-1, 4, 0}, {-4, 4, 0}, {-5, 4, -4}, {0, 4, 4}, {7, 6, 12}, {11, 4, 12},
	}
	for _, c := range cases {
		if got := windowEnd(c.t, c.s); got != c.end {
			t.Fatalf("windowEnd(%d,%d) = %d, want %d", c.t, c.s, got, c.end)
		}
	}
	if got := ceilMultiple(16, 12); got != 24 {
		t.Fatalf("ceilMultiple(16,12) = %d, want 24", got)
	}
	if got := ceilMultiple(12, 12); got != 12 {
		t.Fatalf("ceilMultiple(12,12) = %d, want 12", got)
	}
}
