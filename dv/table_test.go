package dv

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
)

var base = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

const (
	T = 30 * time.Second // 路由超时
	G = 20 * time.Second // 垃圾保持时间
)

func at(sec int) time.Time { return base.Add(time.Duration(sec) * time.Second) }

func ts(t time.Time) string { return t.Format("15:04:05") }

func mustReceive(t *testing.T, tb *Table, now time.Time, n, p string, m int) {
	t.Helper()
	t.Logf("输入: Receive(now=%s, neighbor=%q, prefix=%q, metric=%d)", ts(now), n, p, m)
	if err := tb.Receive(now, n, p, m); err != nil {
		t.Fatalf("输出: 意外拒绝: %v", err)
	}
	t.Logf("输出: 接受, 当前表项=%+v", tb.Snapshot())
}

func mustSweep(t *testing.T, tb *Table, now time.Time) {
	t.Helper()
	t.Logf("输入: Sweep(now=%s)", ts(now))
	if err := tb.Sweep(now); err != nil {
		t.Fatalf("输出: 意外拒绝: %v", err)
	}
	t.Logf("输出: 当前表项=%+v", tb.Snapshot())
}

func findEntry(tb *Table, prefix string) (Entry, bool) {
	for _, e := range tb.Snapshot() {
		if e.Prefix == prefix {
			return e, true
		}
	}
	return Entry{}, false
}

// 当前下一跳通告度量变大仍被接受，并刷新更新时刻。
func TestReceive_CurrentNextHopMetricIncreaseAccepted(t *testing.T) {
	tb := New(T, G)
	mustReceive(t, tb, at(0), "n1", "p", 2)  // c=3
	mustReceive(t, tb, at(10), "n1", "p", 9) // c=10，变大

	e, ok := findEntry(tb, "p")
	if !ok {
		t.Fatal("表项缺失")
	}
	t.Logf("判定依据: n1 是当前下一跳, 规则为总是接受 c(含变大变小)并刷新更新时刻")
	if e.Metric != 10 || e.NextHop != "n1" || !e.UpdatedAt.Equal(at(10)) || e.Garbage {
		t.Fatalf("输出不符: %+v", e)
	}
	t.Logf("输出: metric=10, nextHop=n1, updatedAt=%s, garbage=false —— 符合", ts(e.UpdatedAt))
}

// 其他邻居等值通告不替换；严格更小才替换。
func TestReceive_OtherNeighborEqualMetricNotReplaced(t *testing.T) {
	tb := New(T, G)
	mustReceive(t, tb, at(0), "n1", "p", 2) // c=3
	mustReceive(t, tb, at(5), "n2", "p", 2) // c=3，等值

	e, _ := findEntry(tb, "p")
	t.Logf("判定依据: n2 不是当前下一跳, c=3 不严格小于现有度量 3, 等值不替换且不刷新")
	if e.NextHop != "n1" || e.Metric != 3 || !e.UpdatedAt.Equal(at(0)) {
		t.Fatalf("输出不符: %+v", e)
	}
	t.Logf("输出: nextHop=n1, metric=3, updatedAt=%s —— 等值未替换, 符合", ts(e.UpdatedAt))

	mustReceive(t, tb, at(6), "n2", "p", 1) // c=2 < 3
	e, _ = findEntry(tb, "p")
	t.Logf("判定依据: c=2 严格小于现有度量 3, 替换下一跳并刷新")
	if e.NextHop != "n2" || e.Metric != 2 || !e.UpdatedAt.Equal(at(6)) {
		t.Fatalf("输出不符: %+v", e)
	}
	t.Logf("输出: nextHop=n2, metric=2, updatedAt=%s —— 严格更小已替换, 符合", ts(e.UpdatedAt))
}

// 垃圾项被其他邻居更小度量取代并恢复有效。
func TestReceive_GarbageEntryReplacedBySmallerMetric(t *testing.T) {
	tb := New(T, G)
	mustReceive(t, tb, at(0), "n1", "p", 0) // c=1
	mustSweep(t, tb, at(30))                // UpdatedAt+T=30s 不晚于 now → 转垃圾

	e, _ := findEntry(tb, "p")
	if !e.Garbage || e.Metric != Infinity {
		t.Fatalf("应已进入垃圾期: %+v", e)
	}
	t.Logf("输出: 表项已转垃圾 metric=16 garbageStart=%s", ts(e.GarbageStart))

	mustReceive(t, tb, at(35), "n2", "p", 4) // c=5 < 16
	e, _ = findEntry(tb, "p")
	t.Logf("判定依据: 垃圾项现有度量为 16, c=5 严格小于 16, 替换下一跳并恢复有效")
	if e.Garbage || e.NextHop != "n2" || e.Metric != 5 || !e.UpdatedAt.Equal(at(35)) {
		t.Fatalf("输出不符: %+v", e)
	}
	nh, m, ok := tb.Lookup("p")
	t.Logf("输出: Lookup(p)=(%q, %d, %v) —— 垃圾项已恢复有效, 符合", nh, m, ok)
	if !ok || nh != "n2" || m != 5 {
		t.Fatalf("Lookup 输出不符: %q %d %v", nh, m, ok)
	}
}

// 一次整理可对同一表项连续完成「超时转垃圾」与「垃圾删除」两步。
func TestSweep_CrossesTimeoutAndGarbageInOnePass(t *testing.T) {
	tb := New(T, G)
	mustReceive(t, tb, at(0), "n1", "p", 0) // c=1, UpdatedAt=0s
	mustSweep(t, tb, at(50))                // now = T+G

	t.Logf("判定依据: UpdatedAt+T=30s 不晚于 now=50s 转垃圾, 垃圾起点=30s; 起点+G=50s 不晚于 now, 同一次整理连续完成两步并删除")
	if got := tb.Snapshot(); len(got) != 0 {
		t.Fatalf("输出不符: 表项应被删除, got %+v", got)
	}
	t.Logf("输出: 表项已删除 —— 符合")

	// 对照：只到超时点，转垃圾但不删除。
	tb2 := New(T, G)
	mustReceive(t, tb2, at(0), "n1", "p", 0)
	mustSweep(t, tb2, at(30))
	e, ok := findEntry(tb2, "p")
	t.Logf("判定依据: UpdatedAt+T=30s 不晚于 now=30s 转垃圾; 起点+G=50s 晚于 now, 不删除")
	if !ok || !e.Garbage || e.Metric != Infinity {
		t.Fatalf("输出不符: %+v ok=%v", e, ok)
	}
	t.Logf("输出: 表项处于垃圾期 metric=16 —— 符合")
}

// 垃圾起点为「更新时刻加 T」而非整理时刻。
func TestSweep_GarbageStartIsUpdatedAtPlusTimeout(t *testing.T) {
	tb := New(T, G)
	mustReceive(t, tb, at(0), "n1", "p", 0) // UpdatedAt=0s
	mustSweep(t, tb, at(40))                // now=40s > UpdatedAt+T=30s

	e, _ := findEntry(tb, "p")
	t.Logf("判定依据: 垃圾起点 = UpdatedAt+T = 30s, 而非 now = 40s")
	if !e.Garbage || !e.GarbageStart.Equal(at(30)) || e.Metric != Infinity {
		t.Fatalf("输出不符: %+v", e)
	}
	t.Logf("输出: garbageStart=%s —— 符合", ts(e.GarbageStart))
}

// 当前下一跳对垃圾项再通告 16：垃圾起点不变；以 c<16 通告则恢复有效。
func TestReceive_GarbageKeepsStartOnCurrentNextHop16(t *testing.T) {
	tb := New(T, G)
	mustReceive(t, tb, at(0), "n1", "p", 0)
	mustSweep(t, tb, at(30))                  // 垃圾起点=30s
	mustReceive(t, tb, at(40), "n1", "p", 15) // c=16，已是垃圾

	e, _ := findEntry(tb, "p")
	t.Logf("判定依据: 当前下一跳通告 c=16 而原本已是垃圾, 垃圾起点不变(仍为 30s), 更新时刻刷新")
	if !e.Garbage || !e.GarbageStart.Equal(at(30)) || e.Metric != Infinity || !e.UpdatedAt.Equal(at(40)) {
		t.Fatalf("输出不符: %+v", e)
	}
	t.Logf("输出: garbageStart=%s updatedAt=%s —— 符合", ts(e.GarbageStart), ts(e.UpdatedAt))

	mustReceive(t, tb, at(41), "n1", "p", 3) // c=4 < 16，恢复有效
	e, _ = findEntry(tb, "p")
	t.Logf("判定依据: 当前下一跳通告 c=4, 垃圾项被接受 c<16 后恢复为有效")
	if e.Garbage || e.Metric != 4 || !e.UpdatedAt.Equal(at(41)) {
		t.Fatalf("输出不符: %+v", e)
	}
	t.Logf("输出: garbage=false metric=4 —— 符合")
}

// 水平分割加毒性逆转：下一跳是 x 或垃圾项度量为 16，按前缀升序。
func TestAdvertise_SplitHorizonWithPoisonReverse(t *testing.T) {
	tb := New(T, G)
	mustReceive(t, tb, at(0), "n1", "b", 1)  // c=2 via n1
	mustReceive(t, tb, at(0), "n2", "a", 2)  // c=3 via n2
	mustReceive(t, tb, at(0), "n3", "c", 0)  // c=1 via n3
	mustReceive(t, tb, at(1), "n3", "c", 15) // c=16 → c 项转垃圾

	ads := tb.Advertise("n1")
	want := []Advertisement{
		{Prefix: "a", Metric: 3},
		{Prefix: "b", Metric: Infinity}, // 下一跳是 n1 → 毒性逆转
		{Prefix: "c", Metric: Infinity}, // 垃圾项
	}
	t.Logf("判定依据: 下一跳是 x 或表项为垃圾则度量为 16, 否则为表项度量, 按前缀升序")
	if !reflect.DeepEqual(ads, want) {
		t.Fatalf("输出不符: got %+v want %+v", ads, want)
	}
	t.Logf("输出: Advertise(n1)=%+v —— 符合", ads)

	ads2 := tb.Advertise("n2")
	want2 := []Advertisement{
		{Prefix: "a", Metric: Infinity}, // 下一跳是 n2 → 毒性逆转
		{Prefix: "b", Metric: 2},
		{Prefix: "c", Metric: Infinity},
	}
	if !reflect.DeepEqual(ads2, want2) {
		t.Fatalf("输出不符: got %+v want %+v", ads2, want2)
	}
	t.Logf("输出: Advertise(n2)=%+v —— 符合", ads2)
}

// 度量封顶 16：c=min(m+1,16)，无表项时 c=16 不建项，表项度量恒不超过 16。
func TestMetricCappedAt16(t *testing.T) {
	tb := New(T, G)

	mustReceive(t, tb, at(0), "n1", "p", 15) // c=16
	mustReceive(t, tb, at(1), "n1", "q", 16) // c=min(17,16)=16
	t.Logf("判定依据: 无表项时仅 c<16 才建项, 上述两条 c 均为 16")
	if got := tb.Snapshot(); len(got) != 0 {
		t.Fatalf("输出不符: 不应建项, got %+v", got)
	}
	t.Logf("输出: 未建项 —— 符合")

	mustReceive(t, tb, at(2), "n1", "r", 14) // c=15
	mustReceive(t, tb, at(3), "n1", "r", 15) // c=16 → 转垃圾
	e, _ := findEntry(tb, "r")
	t.Logf("判定依据: 当前下一跳总是接受 c, c=16 转垃圾, 度量恒不超过 16")
	if e.Metric != Infinity || !e.Garbage {
		t.Fatalf("输出不符: %+v", e)
	}
	for _, e := range tb.Snapshot() {
		if e.Metric > Infinity {
			t.Fatalf("度量超过 16: %+v", e)
		}
	}
	t.Logf("输出: r 项 metric=16 garbage=true, 全部表项度量不超过 16 —— 符合")
}

// 拒绝顺序：时钟回拨 → 邻居/前缀为空 → 度量越界；只报第一个且无副作用。
func TestReject_OrderAndNoSideEffects(t *testing.T) {
	tb := New(T, G)
	mustReceive(t, tb, at(10), "n1", "p", 2)
	before := tb.Snapshot()

	cases := []struct {
		name             string
		now              time.Time
		neighbor, prefix string
		metric           int
		want             error
	}{
		{"时钟回拨优先于其他错误", at(9), "", "", 99, ErrClockRollback},
		{"邻居为空优先于前缀与度量", at(10), "", "", 99, ErrEmptyNeighbor},
		{"前缀为空优先于度量", at(10), "n1", "", 99, ErrEmptyPrefix},
		{"度量越界(>16)", at(10), "n1", "p", 17, ErrMetricOutOfRange},
		{"度量越界(<0)", at(10), "n1", "p", -1, ErrMetricOutOfRange},
	}
	for _, c := range cases {
		err := tb.Receive(c.now, c.neighbor, c.prefix, c.metric)
		t.Logf("输入: Receive(now=%s, neighbor=%q, prefix=%q, metric=%d) [%s]",
			ts(c.now), c.neighbor, c.prefix, c.metric, c.name)
		t.Logf("输出: err=%v", err)
		if !errors.Is(err, c.want) {
			t.Fatalf("判定不符: got %v want %v", err, c.want)
		}
	}
	if err := tb.Sweep(at(5)); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("Sweep 应报时钟回拨: %v", err)
	}
	t.Logf("输入: Sweep(now=%s) 早于已见时刻 → 输出: %v", ts(at(5)), ErrClockRollback)

	after := tb.Snapshot()
	t.Logf("判定依据: 被拒绝的操作不得改变表项与更新时刻")
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("输出不符: before=%+v after=%+v", before, after)
	}
	t.Logf("输出: 表项保持不变 %+v —— 符合", after)
}

// 查询下一跳只返回度量小于 16 的有效表项。
func TestLookup_OnlyValidEntries(t *testing.T) {
	tb := New(T, G)
	mustReceive(t, tb, at(0), "n1", "ok", 0)
	mustReceive(t, tb, at(0), "n1", "bad", 0)
	mustReceive(t, tb, at(1), "n1", "bad", 15) // c=16 → 垃圾

	nh, m, ok := tb.Lookup("ok")
	t.Logf("输出: Lookup(ok)=(%q, %d, %v)", nh, m, ok)
	if !ok || nh != "n1" || m != 1 {
		t.Fatalf("输出不符: %q %d %v", nh, m, ok)
	}
	if _, _, ok := tb.Lookup("bad"); ok {
		t.Fatal("判定不符: 垃圾项(度量16)不应被查询返回")
	}
	if _, _, ok := tb.Lookup("missing"); ok {
		t.Fatal("判定不符: 不存在的前缀不应被查询返回")
	}
	t.Logf("判定依据: 只返回度量小于 16 的有效(非垃圾)表项 —— 符合")
}

// 通告、整理、查询与生成可并发调用；每个前缀至多一条表项且度量不超过 16。
func TestConcurrency(t *testing.T) {
	tb := New(T, G)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				now := base.Add(time.Duration(i) * time.Millisecond)
				n := fmt.Sprintf("n%d", w)
				p := fmt.Sprintf("p%d", i%8)
				_ = tb.Receive(now, n, p, i%17) // 部分因时钟回拨被拒, 属预期
				_ = tb.Sweep(now)
				_, _, _ = tb.Lookup(p)
				_ = tb.Advertise(n)
			}
		}(w)
	}
	wg.Wait()

	seen := make(map[string]bool)
	for _, e := range tb.Snapshot() {
		if seen[e.Prefix] {
			t.Fatalf("前缀 %q 出现多条表项", e.Prefix)
		}
		seen[e.Prefix] = true
		if e.Metric > Infinity {
			t.Fatalf("度量超过 16: %+v", e)
		}
	}
	t.Logf("判定依据: 互斥锁保护全部操作, 每个前缀至多一条表项且度量不超过 16 —— 符合, 表项数=%d", len(seen))
}

// 相同操作序列重放结果完全相同。
func TestDeterministicReplay(t *testing.T) {
	type op struct {
		kind           string
		sec            int
		neighbor, pref string
		metric         int
	}
	ops := []op{
		{"recv", 0, "n1", "a", 1},
		{"recv", 0, "n2", "b", 2},
		{"recv", 5, "n1", "a", 5},
		{"recv", 6, "n2", "a", 4},
		{"sweep", 31, "", "", 0},
		{"recv", 32, "n3", "a", 0},
		{"sweep", 60, "", "", 0},
		{"recv", 61, "n1", "c", 15},
		{"sweep", 200, "", "", 0},
	}
	run := func() ([]Entry, []Advertisement) {
		tb := New(T, G)
		for _, o := range ops {
			switch o.kind {
			case "recv":
				if err := tb.Receive(at(o.sec), o.neighbor, o.pref, o.metric); err != nil {
					t.Fatalf("replay Receive: %v", err)
				}
			case "sweep":
				if err := tb.Sweep(at(o.sec)); err != nil {
					t.Fatalf("replay Sweep: %v", err)
				}
			}
		}
		return tb.Snapshot(), tb.Advertise("n1")
	}
	s1, a1 := run()
	s2, a2 := run()
	t.Logf("判定依据: 所有时刻由调用方注入, 相同操作序列重放结果完全相同")
	if !reflect.DeepEqual(s1, s2) || !reflect.DeepEqual(a1, a2) {
		t.Fatalf("输出不符: %+v/%+v vs %+v/%+v", s1, a1, s2, a2)
	}
	t.Logf("输出: 两次重放表项=%+v 通告=%+v —— 一致, 符合", s1, a1)
}
