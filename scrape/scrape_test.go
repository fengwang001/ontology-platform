package scrape_test

import (
	"errors"
	"strings"
	"testing"

	"ontology/head"
	"ontology/query"
	"ontology/scrape"
)

func newEnv(t *testing.T, l, ooo int64, smax, lim int) (*head.Head, *query.Querier, *scrape.Scraper) {
	t.Helper()
	h, err := head.New(l, ooo, smax, lim)
	if err != nil {
		t.Fatal(err)
	}
	return h, query.New(h), scrape.New(h)
}

func fetchOK(m map[string]int64) func() (map[string]int64, error) {
	return func() (map[string]int64, error) { return m, nil }
}

func mustScrape(t *testing.T, s *scrape.Scraper, now int64, target string,
	f func() (map[string]int64, error), want scrape.Result) {
	t.Helper()
	got, err := s.Scrape(now, target, f)
	if err != nil {
		t.Fatalf("Scrape(%d,%q) err = %v", now, target, err)
	}
	if got != want {
		t.Fatalf("Scrape(%d,%q) = %+v, 期望 %+v", now, target, got, want)
	}
	t.Logf("输入 Scrape(%d,%q) 输出 %+v 判定 %+v", now, target, got, want)
}

func checkInstant(t *testing.T, q *query.Querier, name string, ts int64, want query.Result) {
	t.Helper()
	got := q.Instant(name, ts)
	if got != want {
		t.Fatalf("Instant(%q,%d) = %v, 期望 %v", name, ts, got, want)
	}
	t.Logf("输入 Instant(%q,%d) 输出 %v 判定 %v", name, ts, got, want)
}

// 需求中的完整示例：成功抓取、名字消失写标记、失败抓取、超限、恢复。
func TestWorkedExample(t *testing.T) {
	_, q, s := newEnv(t, 300, 100, 100, 2)
	stale, absent := query.Result{Kind: query.Stale}, query.Result{Kind: query.Absent}
	value := func(v int64) query.Result { return query.Result{Kind: query.Value, V: v} }

	mustScrape(t, s, 1000, "t", fetchOK(map[string]int64{"a": 5, "b": 7}), scrape.Result{Up: true, Reason: scrape.Ok})
	checkInstant(t, q, "t/a", 1000, value(5))
	checkInstant(t, q, "t/b", 1000, value(7))
	checkInstant(t, q, "t/up", 1000, value(1))

	mustScrape(t, s, 1200, "t", fetchOK(map[string]int64{"a": 6}), scrape.Result{Up: true, Reason: scrape.Ok})
	checkInstant(t, q, "t/b", 1199, value(7)) // 差 199 < L
	checkInstant(t, q, "t/b", 1200, stale)    // 消失的名字写标记
	checkInstant(t, q, "t/b", 1350, stale)
	checkInstant(t, q, "t/a", 1499, value(6))
	checkInstant(t, q, "t/a", 1500, absent) // 差恰等 L=300

	mustScrape(t, s, 1300, "t", func() (map[string]int64, error) {
		return nil, errors.New("boom")
	}, scrape.Result{Up: false, Reason: scrape.Error})
	checkInstant(t, q, "t/a", 1300, stale) // 失败后 prev 中名字一律 Stale
	checkInstant(t, q, "t/up", 1300, value(0))

	mustScrape(t, s, 1400, "t", fetchOK(map[string]int64{"a": 1, "b": 2, "c": 3}),
		scrape.Result{Up: false, Reason: scrape.Limit}) // 3 个名字超过 Lim=2
	checkInstant(t, q, "t/up", 1400, value(0))
	checkInstant(t, q, "t/a", 1400, stale) // prev 已空, 无新标记, 仍是 1300 的标记

	mustScrape(t, s, 1500, "t", fetchOK(map[string]int64{"a": 9, "b": 1}), scrape.Result{Up: true, Reason: scrape.Ok})
	checkInstant(t, q, "t/b", 1500, value(1))
}

// 时钟回退：now 必须严格大于上次被接受的 now; 被拒绝时不调用 fetch。
func TestClockRegression(t *testing.T) {
	_, _, s := newEnv(t, 300, 100, 100, 2)
	mustScrape(t, s, 1000, "t", fetchOK(map[string]int64{"a": 1}), scrape.Result{Up: true})
	called := false
	spy := func() (map[string]int64, error) { called = true; return map[string]int64{"a": 2}, nil }
	for _, now := range []int64{1000, 999} {
		if _, err := s.Scrape(now, "t", spy); !errors.Is(err, scrape.ErrClock) {
			t.Fatalf("Scrape(%d) err = %v, 期望 ErrClock", now, err)
		}
	}
	if called {
		t.Fatal("时钟回退被拒绝时不应调用 fetch")
	}
	mustScrape(t, s, 1001, "t", spy, scrape.Result{Up: true}) // 严格更大可接受
}

// 参数非法先于时钟回退判定。
func TestInvalidParamPrecedence(t *testing.T) {
	_, _, s := newEnv(t, 300, 100, 100, 2)
	mustScrape(t, s, 1000, "t", fetchOK(map[string]int64{"a": 1}), scrape.Result{Up: true})
	cases := []struct {
		name   string
		now    int64
		target string
		f      func() (map[string]int64, error)
	}{
		{"nil fetch", 999, "t", nil},
		{"空 target", 999, "", fetchOK(nil)},
		{"超长 target", 999, strings.Repeat("x", 65), fetchOK(nil)},
		{"now 越界", -1, "t", fetchOK(nil)},
	}
	for _, c := range cases {
		if _, err := s.Scrape(c.now, c.target, c.f); !errors.Is(err, scrape.ErrInvalidParam) {
			t.Fatalf("%s: err = %v, 期望 ErrInvalidParam (先于时钟回退)", c.name, err)
		}
		t.Logf("输入 %s 输出 ErrInvalidParam 判定: 参数非法先于时钟回退", c.name)
	}
}

// 失败原因：Panic、Error、Limit、Invalid 各就各位, up=0 且 prev 变空。
func TestFailureReasons(t *testing.T) {
	_, q, s := newEnv(t, 300, 100, 100, 2)
	mustScrape(t, s, 1000, "t", fetchOK(map[string]int64{"m": 1}), scrape.Result{Up: true, Reason: scrape.Ok})
	mustScrape(t, s, 1100, "t", func() (map[string]int64, error) { panic("kaboom") },
		scrape.Result{Up: false, Reason: scrape.Panic})
	checkInstant(t, q, "t/m", 1100, query.Result{Kind: query.Stale})
	checkInstant(t, q, "t/up", 1100, query.Result{Kind: query.Value, V: 0})
	mustScrape(t, s, 1200, "t", fetchOK(map[string]int64{"a": 1, "b": 2, "c": 3}),
		scrape.Result{Up: false, Reason: scrape.Limit}) // 个数超 Lim 先于名字检查
	for i, m := range []map[string]int64{{"": 1}, {"up": 1}, {strings.Repeat("n", 64): 1}} {
		mustScrape(t, s, int64(1300+i), "t", fetchOK(m), scrape.Result{Up: false, Reason: scrape.Invalid})
	}
	mustScrape(t, s, 1400, "t", fetchOK(map[string]int64{"": 1, "a": 1, "b": 2}),
		scrape.Result{Up: false, Reason: scrape.Limit}) // 超限优先于逐个名字检查
	t.Logf("判定: Panic>Error>Limit>Invalid; 名字为空/等于up/超过63字节为 Invalid")
}

// 连续失败不重复产生标记：第一次失败后 prev 已空, 第二次只写 up=0。
func TestConsecutiveFailures(t *testing.T) {
	h, q, s := newEnv(t, 300, 100, 100, 2)
	fail := func() (map[string]int64, error) { return nil, errors.New("x") }
	mustScrape(t, s, 1000, "f", fetchOK(map[string]int64{"m": 1}), scrape.Result{Up: true})
	mustScrape(t, s, 2000, "f", fail, scrape.Result{Up: false, Reason: scrape.Error})
	dups := h.Dups
	mustScrape(t, s, 3000, "f", fail, scrape.Result{Up: false, Reason: scrape.Error})
	if h.Dups != dups {
		t.Fatalf("连续失败产生重复写入: Dups %d -> %d", dups, h.Dups)
	}
	checkInstant(t, q, "f/m", 2100, query.Result{Kind: query.Stale})  // 仍是 2000 的标记
	checkInstant(t, q, "f/m", 3000, query.Result{Kind: query.Absent}) // 标记本身也超窗
	checkInstant(t, q, "f/up", 3000, query.Result{Kind: query.Value, V: 0})
	t.Logf("判定: 第一次失败写标记并清空 prev, 第二次失败 prev 为空只写 up=0")
}

// 预校验：写集中任一项过旧则整个抓取不写, lastNow 不变可重试。
func TestPrevalidationAllOrNothing(t *testing.T) {
	h, q, s := newEnv(t, 300, 100, 100, 2)
	if err := h.Append("t2/a", 5000, 1); err != nil {
		t.Fatal(err)
	}
	_, err := s.Scrape(4000, "t2", fetchOK(map[string]int64{"a": 1}))
	if !errors.Is(err, head.ErrTooOld) {
		t.Fatalf("Scrape err = %v, 期望 ErrTooOld", err)
	}
	checkInstant(t, q, "t2/up", 4000, query.Result{Kind: query.Absent}) // up 未写
	checkInstant(t, q, "t2/a", 5000, query.Result{Kind: query.Value, V: 1})
	if _, err := s.Scrape(4000, "t2", fetchOK(map[string]int64{"a": 1})); !errors.Is(err, head.ErrTooOld) {
		t.Fatalf("重试 err = %v, 期望 ErrTooOld (证明 lastNow 未变)", err)
	}
	mustScrape(t, s, 5001, "t2", fetchOK(map[string]int64{"a": 2}), scrape.Result{Up: true})
	t.Logf("判定: 预校验失败整体不写, lastNow 不变, 之后合法抓取成功")
}

// 序列上限：新 target 的 up 序列也算新序列, 超限则整个抓取不写。
func TestSeriesLimitBlocksNewTarget(t *testing.T) {
	_, q, s := newEnv(t, 300, 100, 2, 10)
	mustScrape(t, s, 1000, "a", fetchOK(map[string]int64{"x": 1}), scrape.Result{Up: true}) // 占满 a/x, a/up
	_, err := s.Scrape(1000, "b", fetchOK(map[string]int64{"y": 1}))
	if !errors.Is(err, head.ErrTooManySeries) {
		t.Fatalf("Scrape err = %v, 期望 ErrTooManySeries", err)
	}
	checkInstant(t, q, "b/up", 1000, query.Result{Kind: query.Absent})
	checkInstant(t, q, "b/y", 1000, query.Result{Kind: query.Absent})
	if _, err := s.Scrape(1000, "b", func() (map[string]int64, error) { return nil, errors.New("x") }); !errors.Is(err, head.ErrTooManySeries) {
		t.Fatalf("失败抓取 err = %v, 期望 ErrTooManySeries", err)
	}
	t.Logf("判定: 序列上限下新 target 的 up 序列无法创建, 整个抓取不写")
}
