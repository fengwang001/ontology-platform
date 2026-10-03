package reconcile

import (
	"errors"
	"reflect"
	"testing"
)

func imp(user, ad string, t, v int64) Event {
	return Event{Kind: Impression, User: user, Ad: ad, T: t, V: v}
}

func clk(user, ad string, t int64) Event {
	return Event{Kind: Click, User: user, Ad: ad, T: t}
}

func mustNew(t *testing.T, v, c, a, tmin, lk, tu int64) *Reconciler {
	t.Helper()
	r, err := New(v, c, a, tmin, lk, tu)
	if err != nil {
		t.Fatalf("New(%d,%d,%d,%d,%d,%d) 报错: %v", v, c, a, tmin, lk, tu, err)
	}
	return r
}

// submit 提交一批并断言结论序列（事件需已按排序后次序给出）。
func submit(t *testing.T, r *Reconciler, events []Event, want []Outcome) {
	t.Helper()
	got, err := r.Submit(events)
	if err != nil {
		t.Fatalf("Submit(%v) 报错: %v", events, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Submit(%v) = %v, 期望 %v", events, got, want)
	}
}

func checkStats(t *testing.T, r *Reconciler, want Stats) {
	t.Helper()
	if got := r.Stats(); got != want {
		t.Fatalf("Stats() = %+v, 期望 %+v", got, want)
	}
}

// TestSpecExample 完整复现题目示例：Tu=0 与 Tu=100 两种情形。
func TestSpecExample(t *testing.T) {
	events := []Event{
		imp("u", "a", 0, 1500),   // 已计数
		imp("u", "a", 30, 2000),  // 冷却：30-0 < 60
		clk("u", "a", 40),        // 归因：g=40，e=90
		imp("u", "a", 60, 500),   // 不可见：500 < 1000
		imp("u", "a", 60, 1000),  // 冷却：60 < e=90
		clk("u", "a", 61),        // 重复
		imp("u", "a", 90, 1200),  // 已计数：90 == e 不再锁定
		clk("u", "a", 91),        // 过快：g=1 < 2
		clk("u", "a", 92),        // Tu=0 归因 e=142；Tu=100 串扰
		clk("u", "a", 93),        // Tu=0 重复；Tu=100 串扰
		imp("u", "a", 141, 3000), // 冷却：141-90=51 < 60
		clk("u", "a", 391),       // 已过期：g=301 > 300
	}

	r0 := mustNew(t, 1000, 60, 300, 2, 50, 0)
	submit(t, r0, events, []Outcome{
		Counted, Cooldown, Attributed, Invisible, Cooldown, Duplicate,
		Counted, TooFast, Attributed, Duplicate, Cooldown, Expired,
	})
	checkStats(t, r0, Stats{
		Counted: 2, Cooldown: 3, Invisible: 1,
		Attributed: 2, Duplicate: 2, Expired: 1, TooFast: 1,
	})

	r100 := mustNew(t, 1000, 60, 300, 2, 50, 100)
	submit(t, r100, events, []Outcome{
		Counted, Cooldown, Attributed, Invisible, Cooldown, Duplicate,
		Counted, TooFast, CrossTalk, CrossTalk, Cooldown, Expired,
	})
	checkStats(t, r100, Stats{
		Counted: 2, Cooldown: 3, Invisible: 1,
		Attributed: 1, Duplicate: 1, Expired: 1, TooFast: 1, CrossTalk: 2,
	})
}

// TestVisibilityBoundary v 恰等于 V 可见，差 1 不可见。
func TestVisibilityBoundary(t *testing.T) {
	r := mustNew(t, 1000, 0, 300, 0, 0, 0)
	submit(t, r, []Event{imp("u", "a", 0, 1000)}, []Outcome{Counted})
	submit(t, r, []Event{imp("u", "a", 10, 999)}, []Outcome{Invisible})
}

// TestCooldownBoundary t-锚点恰等于 C 已计数，差 1 冷却。
func TestCooldownBoundary(t *testing.T) {
	r := mustNew(t, 0, 60, 300, 0, 0, 0)
	submit(t, r, []Event{
		imp("u", "a", 0, 10),
		imp("u", "a", 59, 10),
		imp("u", "a", 60, 10),
	}, []Outcome{Counted, Cooldown, Counted})
}

// TestLockBoundary t 恰等于 e 不再锁定，差 1 仍锁定。
func TestLockBoundary(t *testing.T) {
	r := mustNew(t, 0, 0, 300, 0, 50, 0)
	submit(t, r, []Event{
		imp("u", "a", 0, 10),
		clk("u", "a", 40), // 归因，e=90
		imp("u", "a", 89, 10),
		imp("u", "a", 90, 10),
	}, []Outcome{Counted, Attributed, Cooldown, Counted})
}

// TestLockOnlyFromAttribution 锁定只由归因产生：过快与重复不延长 e。
func TestLockOnlyFromAttribution(t *testing.T) {
	// 过快不产生锁定：Tmin=5，点击 g=2 过快，若误设 e=2+50=52，
	// 则 t=10 的曝光会被锁定；实际应已计数。
	r1 := mustNew(t, 0, 0, 300, 5, 50, 0)
	submit(t, r1, []Event{
		imp("u", "a", 0, 10),
		clk("u", "a", 2),
		imp("u", "a", 10, 10),
	}, []Outcome{Counted, TooFast, Counted})

	// 重复不延长锁定：归因于 t=40，e=90；t=50 的重复若误延长 e 至 100，
	// 则 t=95 的曝光会被锁定；实际应已计数。
	r2 := mustNew(t, 0, 0, 300, 0, 50, 0)
	submit(t, r2, []Event{
		imp("u", "a", 0, 10),
		clk("u", "a", 40),
		clk("u", "a", 50),
		imp("u", "a", 95, 10),
	}, []Outcome{Counted, Attributed, Duplicate, Counted})
}

// TestInvisibleAndCooldownDontMoveAnchor 不可见与冷却的曝光都不移动锚点，
// 冷却曝光之后的点击仍归给更早的锚点且窗口按锚点起算。
func TestInvisibleAndCooldownDontMoveAnchor(t *testing.T) {
	r := mustNew(t, 1000, 60, 50, 0, 0, 0)
	submit(t, r, []Event{
		imp("u", "a", 0, 1500),  // 已计数，锚点=0
		imp("u", "a", 10, 500),  // 不可见，不动锚点
		imp("u", "a", 30, 2000), // 冷却，不动锚点
		clk("u", "a", 51),       // g=51 > A=50：按锚点 0 起算，已过期
	}, []Outcome{Counted, Invisible, Cooldown, Expired})

	// 对照：g 恰按锚点 0 起算，t=50 时 g=50 仍有效。
	r2 := mustNew(t, 1000, 60, 50, 0, 0, 0)
	submit(t, r2, []Event{
		imp("u", "a", 0, 1500),
		imp("u", "a", 30, 2000), // 冷却
		clk("u", "a", 50),       // g=50 == A，归因
	}, []Outcome{Counted, Cooldown, Attributed})
}

// TestAttributionWindowBoundary g 恰等于 A 有效，差 1 过期。
func TestAttributionWindowBoundary(t *testing.T) {
	r := mustNew(t, 0, 0, 300, 0, 0, 0)
	submit(t, r, []Event{
		imp("u", "a", 0, 10),
		imp("u", "b", 0, 10),
		clk("u", "a", 300),
		clk("u", "b", 301),
	}, []Outcome{Counted, Counted, Attributed, Expired})
}

// TestMinIntervalBoundary g 恰等于 Tmin 有效，差 1 过快。
func TestMinIntervalBoundary(t *testing.T) {
	r := mustNew(t, 0, 0, 300, 5, 0, 0)
	submit(t, r, []Event{
		imp("u", "a", 0, 10),
		clk("u", "a", 4),
		clk("u", "a", 5),
	}, []Outcome{Counted, TooFast, Attributed})
}

// TestJudgmentOrder 已过期 -> 过快 -> 重复 -> 串扰，只报第一个。
func TestJudgmentOrder(t *testing.T) {
	// 已过期先于过快：Tmin=100 > A=10，g=50 同时满足两者，报已过期。
	r1 := mustNew(t, 0, 0, 10, 100, 0, 0)
	submit(t, r1, []Event{
		imp("u", "a", 0, 10),
		clk("u", "a", 50),
	}, []Outcome{Counted, Expired})

	// 已过期先于重复：锚点已被归因，g > A 时报已过期而非重复。
	r2 := mustNew(t, 0, 0, 10, 0, 0, 0)
	submit(t, r2, []Event{
		imp("u", "a", 0, 10),
		clk("u", "a", 5),
		clk("u", "a", 50),
	}, []Outcome{Counted, Attributed, Expired})

	// 重复先于串扰：锚点已被归因且 t-u < Tu，报重复而非串扰。
	r4 := mustNew(t, 0, 0, 300, 0, 0, 1000)
	submit(t, r4, []Event{
		imp("u", "a", 0, 10),
		clk("u", "a", 10),
		clk("u", "a", 20),
	}, []Outcome{Counted, Attributed, Duplicate})
}

// TestTooFastBeforeDuplicateByConstruction 过快判定位于重复之前：
// 由于水位单调，同对后续点击的 g 不会变小，该组合只能在新锚点未归因时
// 出现过快；这里直接构造“若次序颠倒会报重复”的场景验证过快优先。
func TestTooFastBeforeDuplicateByConstruction(t *testing.T) {
	// 锚点已归因后，同对点击的 g 只增不减，不可能再过快；
	// 等价场景：g < Tmin 时即使锚点状态如何都报过快。
	r := mustNew(t, 0, 0, 300, 10, 0, 0)
	submit(t, r, []Event{
		imp("u", "a", 0, 10),
		clk("u", "a", 5),  // g=5 < Tmin=10：过快，且不标已归因
		clk("u", "a", 15), // g=15 有效：归因（证明过快未标已归因）
	}, []Outcome{Counted, TooFast, Attributed})
}

// TestUserIntervalBoundary t-u 恰等于 Tu 可归因，差 1 串扰。
func TestUserIntervalBoundary(t *testing.T) {
	r := mustNew(t, 0, 0, 1000, 0, 0, 100)
	submit(t, r, []Event{
		imp("u", "a", 0, 10),
		clk("u", "a", 10), // 归因，u=10
		imp("u", "b", 20, 10),
		imp("u", "c", 30, 10),
		clk("u", "b", 109), // t-u=99 < 100：串扰
		clk("u", "c", 110), // t-u=100 == Tu：归因
	}, []Outcome{Counted, Attributed, Counted, Counted, CrossTalk, Attributed})
}

// TestCrossBatchTLessThanU t < u（跨批、不同 ad）也是串扰。
func TestCrossBatchTLessThanU(t *testing.T) {
	r := mustNew(t, 0, 0, 1000, 0, 0, 10)
	submit(t, r, []Event{
		imp("u", "a", 100, 10),
		clk("u", "a", 100), // 归因，u=100
	}, []Outcome{Counted, Attributed})
	// 另一对的水位独立，t=50 合法；但 t=50 < u=100，t-u=-50 < Tu，串扰。
	submit(t, r, []Event{
		imp("u", "b", 50, 10),
		clk("u", "b", 50),
	}, []Outcome{Counted, CrossTalk})
}

// TestCrosstalkNoStateChange 串扰不标已归因也不改 e。
func TestCrosstalkNoStateChange(t *testing.T) {
	r := mustNew(t, 0, 0, 1000, 0, 10, 100)
	submit(t, r, []Event{
		imp("u", "a", 0, 10),
		clk("u", "a", 10), // 归因，(u,a) 的 e=20，u=10
	}, []Outcome{Counted, Attributed})
	submit(t, r, []Event{
		imp("u", "b", 0, 10),
		clk("u", "b", 50), // 串扰：t-u=40 < 100
		// 串扰未标已归因：下一击仍是串扰而非重复。
		clk("u", "b", 55), // 串扰：t-u=45 < 100
	}, []Outcome{Counted, CrossTalk, CrossTalk})
	// 串扰未改 e：若误设 e=50+10=60，t=55 的曝光会被锁定。
	submit(t, r, []Event{
		imp("u", "b", 55, 10),
	}, []Outcome{Counted})
}

// TestTuZero Tu=0 时退化：t >= u 的点击永不串扰。
func TestTuZero(t *testing.T) {
	r := mustNew(t, 0, 0, 1000, 0, 0, 0)
	submit(t, r, []Event{
		imp("u", "a", 0, 10),
		clk("u", "a", 10), // 归因，u=10
		imp("u", "b", 20, 10),
		clk("u", "b", 20), // t-u=10 不小于 0：归因
	}, []Outcome{Counted, Attributed, Counted, Attributed})
}

// TestSameTimestampImpressionFirst 同刻曝光先于点击处理。
func TestSameTimestampImpressionFirst(t *testing.T) {
	r := mustNew(t, 0, 0, 300, 0, 0, 0)
	// 输入点击在前，排序后曝光先处理，点击可归因（g=0 >= Tmin=0）。
	got, err := r.Submit([]Event{clk("u", "a", 5), imp("u", "a", 5, 10)})
	if err != nil {
		t.Fatalf("Submit 报错: %v", err)
	}
	// 返回值按排序后次序：曝光、点击。
	if want := []Outcome{Counted, Attributed}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, 期望 %v", got, want)
	}
	// 同刻同类型保持输入次序（稳定排序）。
	r2 := mustNew(t, 0, 0, 300, 0, 0, 0)
	got, err = r2.Submit([]Event{imp("u", "a", 5, 1), imp("u", "a", 5, 0)})
	if err != nil {
		t.Fatalf("Submit 报错: %v", err)
	}
	if want := []Outcome{Counted, Counted}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, 期望 %v", got, want)
	}
}

// TestBatchSortedAndCrossBatchReject 批内输入乱序但排序后合法；
// 跨批 t 小于水位整批拒绝且不改状态。
func TestBatchSortedAndCrossBatchReject(t *testing.T) {
	r := mustNew(t, 0, 0, 300, 0, 0, 0)
	// 批内输入乱序，排序后合法。
	submit(t, r, []Event{
		imp("u", "a", 10, 10),
		imp("u", "a", 5, 10),
	}, []Outcome{Counted, Counted}) // 排序后：t=5 先，t=10 后
	before := r.Stats()
	// 跨批 t=7 小于水位 10：整批拒绝。
	_, err := r.Submit([]Event{imp("u", "a", 7, 10), imp("u", "a", 20, 10)})
	if !errors.Is(err, ErrOutOfOrder) {
		t.Fatalf("err = %v, 期望 ErrOutOfOrder", err)
	}
	if got := r.Stats(); got != before {
		t.Fatalf("拒绝后状态被改变: %+v -> %+v", before, got)
	}
	// 拒绝不影响后续合法批。
	submit(t, r, []Event{clk("u", "a", 15)}, []Outcome{Attributed})
}

// TestTEqualsWatermarkAccepted t 等于水位被接受。
func TestTEqualsWatermarkAccepted(t *testing.T) {
	r := mustNew(t, 0, 0, 300, 0, 0, 0)
	submit(t, r, []Event{imp("u", "a", 10, 10)}, []Outcome{Counted})
	submit(t, r, []Event{imp("u", "a", 10, 10)}, []Outcome{Counted})
}

// TestPairsIndependent 不同对互不影响。
func TestPairsIndependent(t *testing.T) {
	r := mustNew(t, 0, 60, 300, 0, 50, 0)
	submit(t, r, []Event{
		imp("u1", "a1", 0, 10),
		clk("u1", "a1", 10),     // 归因，(u1,a1) 锁定至 60
		imp("u1", "a2", 20, 10), // 不同 ad：不受锁定影响
		imp("u2", "a1", 20, 10), // 不同 user：不受锁定影响
		imp("u1", "a1", 20, 10), // 同对：t=20 < e=60，冷却
	}, []Outcome{Counted, Attributed, Counted, Counted, Cooldown})
}

// TestNoImpression 无曝光的点击；不可见/冷却曝光不产生锚点。
func TestNoImpression(t *testing.T) {
	r := mustNew(t, 1000, 60, 300, 0, 0, 0)
	submit(t, r, []Event{
		clk("u", "a", 0),        // 无曝光
		imp("u", "a", 10, 500),  // 不可见，不产生锚点
		clk("u", "a", 20),       // 仍无曝光
		imp("u", "a", 30, 1500), // 已计数
		imp("u", "a", 40, 1500), // 冷却，不产生锚点
		clk("u", "b", 40),       // 另一对无曝光
	}, []Outcome{NoImpression, Invisible, NoImpression, Counted, Cooldown, NoImpression})
}

// TestInvalidParams 参数非法：构造参数越界、user/ad 为空、t/v 越界，整批拒绝。
func TestInvalidParams(t *testing.T) {
	for _, p := range [][6]int64{
		{-1, 0, 0, 0, 0, 0}, {1e9 + 1, 0, 0, 0, 0, 0},
		{0, -1, 0, 0, 0, 0}, {0, 0, 1e9 + 1, 0, 0, 0},
		{0, 0, 0, 0, 0, 1e9 + 1},
	} {
		if _, err := New(p[0], p[1], p[2], p[3], p[4], p[5]); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("New%v err = %v, 期望 ErrInvalidParam", p, err)
		}
	}
	// 边界值合法。
	if _, err := New(0, 0, 0, 0, 0, 0); err != nil {
		t.Fatalf("New 全 0 报错: %v", err)
	}
	if _, err := New(1e9, 1e9, 1e9, 1e9, 1e9, 1e9); err != nil {
		t.Fatalf("New 全 1e9 报错: %v", err)
	}

	r := mustNew(t, 0, 0, 300, 0, 0, 0)
	submit(t, r, []Event{imp("u", "a", 100, 10)}, []Outcome{Counted})
	before := r.Stats()

	bad := [][]Event{
		{imp("", "a", 200, 10)},                       // user 为空
		{imp("u", "", 200, 10)},                       // ad 为空
		{clk("u", "a", -1)},                           // t 越界
		{clk("u", "a", 1e15+1)},                       // t 越界
		{imp("u", "a", 200, -1)},                      // v 越界
		{imp("u", "a", 200, 1e9+1)},                   // v 越界
		{{Kind: Kind(7), User: "u", Ad: "a", T: 200}}, // 未知类型
		{imp("u", "a", 200, 10), clk("", "a", 300)},   // 一条非法整批拒绝
	}
	for i, evs := range bad {
		if _, err := r.Submit(evs); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("bad[%d] err = %v, 期望 ErrInvalidParam", i, err)
		}
	}
	if got := r.Stats(); got != before {
		t.Fatalf("非法批改变了状态: %+v -> %+v", before, got)
	}
	// 边界值合法：t=0、t=1e15、v=0、v=1e9。
	r2 := mustNew(t, 0, 0, 1e9, 0, 0, 0)
	submit(t, r2, []Event{
		imp("u", "a", 0, 0),
		clk("u", "a", 0),
		imp("u", "b", 1e15-100, 1e9),
		clk("u", "b", 1e15),
	}, []Outcome{Counted, Attributed, Counted, Attributed})
}

// TestStatsInvariants 计数恒等式与归因上界。
func TestStatsInvariants(t *testing.T) {
	r := mustNew(t, 1000, 60, 300, 2, 50, 100)
	events := []Event{
		imp("u", "a", 0, 1500), imp("u", "a", 30, 2000), clk("u", "a", 40),
		imp("u", "a", 60, 500), imp("u", "a", 60, 1000), clk("u", "a", 61),
		imp("u", "a", 90, 1200), clk("u", "a", 91), clk("u", "a", 92),
		clk("u", "a", 93), imp("u", "a", 141, 3000), clk("u", "a", 391),
		clk("u", "b", 5),
	}
	if _, err := r.Submit(events); err != nil {
		t.Fatalf("Submit 报错: %v", err)
	}
	s := r.Stats()
	total := s.Counted + s.Cooldown + s.Invisible + s.Attributed + s.Duplicate +
		s.Expired + s.TooFast + s.NoImpression + s.CrossTalk
	if total != int64(len(events)) {
		t.Fatalf("总计数 %d != 事件数 %d", total, len(events))
	}
	if s.Attributed > s.Counted {
		t.Fatalf("归因数 %d 超过已计数曝光数 %d", s.Attributed, s.Counted)
	}
}

// TestReplayDeterminism 相同的批序列重放得到完全相同的结论序列与计数。
func TestReplayDeterminism(t *testing.T) {
	batches := [][]Event{
		{imp("u", "a", 0, 1500), clk("u", "a", 40), imp("u", "b", 10, 2000)},
		{clk("u", "b", 20), imp("u", "a", 100, 1000), clk("u", "a", 120)},
		{clk("u", "a", 500)},
	}
	run := func() ([]Outcome, Stats) {
		r := mustNew(t, 1000, 60, 300, 2, 50, 100)
		var all []Outcome
		for _, b := range batches {
			got, err := r.Submit(b)
			if err != nil {
				t.Fatalf("Submit 报错: %v", err)
			}
			all = append(all, got...)
		}
		return all, r.Stats()
	}
	o1, s1 := run()
	o2, s2 := run()
	if !reflect.DeepEqual(o1, o2) || s1 != s2 {
		t.Fatalf("重放不一致: %v/%+v vs %v/%+v", o1, s1, o2, s2)
	}
}
