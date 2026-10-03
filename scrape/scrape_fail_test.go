package scrape_test

import (
	"errors"
	"strings"
	"testing"

	"ontology/head"
	"ontology/query"
	"ontology/scrape"
)

// TestFailureReasons 覆盖 Panic、Error、Limit、Invalid 及其优先级。
func TestFailureReasons(t *testing.T) {
	_, q, s := setup(t, 1000, 0, 100, 2)
	cases := []struct {
		name  string
		now   int64
		fetch func() (map[string]int64, error)
		want  scrape.Reason
	}{
		{"panic 优先于一切", 100, func() (map[string]int64, error) { panic("kaboom") }, scrape.Panic},
		{"error", 200, errFetch(errors.New("boom")), scrape.Error},
		{"error 优先于 limit", 300, func() (map[string]int64, error) {
			return map[string]int64{"a": 1, "b": 2, "c": 3}, errors.New("x")
		}, scrape.Error},
		{"个数超限先于名字检查", 400, okFetch(map[string]int64{"": 1, "b": 2, "c": 3}), scrape.Limit},
		{"空名字", 500, okFetch(map[string]int64{"": 1}), scrape.Invalid},
		{"名字为 up", 600, okFetch(map[string]int64{"up": 1}), scrape.Invalid},
		{"名字超 63 字节", 700, okFetch(map[string]int64{strings.Repeat("n", 64): 1}), scrape.Invalid},
		{"63 字节名字合法", 800, okFetch(map[string]int64{strings.Repeat("n", 63): 1}), scrape.Ok},
	}
	for _, c := range cases {
		r, err := s.Scrape(c.now, "f", c.fetch)
		t.Logf("输入 Scrape(%d,f) [%s] 输出 %+v err=%v", c.now, c.name, r, err)
		if err != nil || r.Reason != c.want {
			t.Fatalf("%s: 期望 Reason=%d，得到 %+v err=%v", c.name, c.want, r, err)
		}
		if c.want != scrape.Ok && r.Up != 0 {
			t.Fatalf("%s: 失败时 Up 应为 0", c.name)
		}
	}
	wantInstant(t, q, "f/up", 800, query.Result{Kind: query.Value, V: 1})
	t.Logf("判定依据: 失败原因按 Panic、Error、Limit、Invalid 顺序取第一个成立者")
}

// TestConsecutiveFailures 连续失败不重复产生标记。
func TestConsecutiveFailures(t *testing.T) {
	h, _, s := setup(t, 10000, 0, 100, 5)
	_, _ = s.Scrape(100, "g", okFetch(map[string]int64{"a": 1, "b": 2}))
	_, _ = s.Scrape(200, "g", errFetch(errors.New("x"))) // prev={a,b} → 两个标记
	_, _ = s.Scrape(300, "g", errFetch(errors.New("y"))) // prev=空 → 无标记
	_, _ = s.Scrape(400, "g", func() (map[string]int64, error) { panic("p") })
	snap := h.Snapshot()
	for _, name := range []string{"g/a", "g/b"} {
		got := snap[name]
		if len(got) != 2 || !got[1].Stale || got[1].Ts != 200 {
			t.Fatalf("%s: 期望 1 个活样本 + 1 个 200 处标记，得到 %v", name, got)
		}
	}
	if up := snap["g/up"]; len(up) != 4 {
		t.Fatalf("up 应有 4 个样本: %v", up)
	}
	t.Logf("判定依据: 失败使 prev 清空，后续失败只写 up=0，不再产生标记")
}

// TestDisappearedOnSuccess 成功抓取中消失的名字写标记，结果里的名字读到新值。
func TestDisappearedOnSuccess(t *testing.T) {
	_, q, s := setup(t, 1000, 0, 100, 5)
	_, _ = s.Scrape(100, "d", okFetch(map[string]int64{"a": 1, "b": 2, "c": 3}))
	_, _ = s.Scrape(200, "d", okFetch(map[string]int64{"b": 20}))
	wantInstant(t, q, "d/a", 199, query.Result{Kind: query.Value, V: 1})
	wantInstant(t, q, "d/a", 200, query.Result{Kind: query.Stale})
	wantInstant(t, q, "d/c", 250, query.Result{Kind: query.Stale})
	wantInstant(t, q, "d/b", 200, query.Result{Kind: query.Value, V: 20})
	t.Logf("判定依据: 成功后 prev 中不在结果里的名字写陈旧标记，结果里的名字写本次值")
}

// TestClockAndParamOrder 参数非法先于时钟回退；被拒绝时不调用 fetch。
func TestClockAndParamOrder(t *testing.T) {
	_, _, s := setup(t, 1000, 0, 100, 5)
	if _, err := s.Scrape(100, "ck", okFetch(map[string]int64{"a": 1})); err != nil {
		t.Fatal(err)
	}
	called := false
	spy := func() (map[string]int64, error) { called = true; return nil, nil }
	if _, err := s.Scrape(100, "ck", spy); err != scrape.ErrClockRegression {
		t.Fatalf("now 相等: 期望 ErrClockRegression，得到 %v", err)
	}
	if _, err := s.Scrape(99, "ck", spy); err != scrape.ErrClockRegression {
		t.Fatalf("now 回退: 期望 ErrClockRegression，得到 %v", err)
	}
	if called {
		t.Fatal("时钟回退被拒绝时不应调用 fetch")
	}
	if _, err := s.Scrape(50, "ck", nil); err != scrape.ErrInvalidParam {
		t.Fatalf("nil fetch 应先于时钟判定: 得到 %v", err)
	}
	if _, err := s.Scrape(50, "", spy); err != scrape.ErrInvalidParam {
		t.Fatalf("空 target 应先于时钟判定: 得到 %v", err)
	}
	if _, err := s.Scrape(50, strings.Repeat("t", 65), spy); err != scrape.ErrInvalidParam {
		t.Fatalf("target 超 64 字节: 得到 %v", err)
	}
	if _, err := s.Scrape(200, "ck", spy); err != nil || !called {
		t.Fatalf("合法抓取应成功并调用 fetch: err=%v called=%v", err, called)
	}
	t.Logf("判定依据: fetch 为 nil 或 target 不合法为参数非法，先于时钟回退判定；被拒绝时不调用 fetch")
}

// TestSeriesLimitBlocksNewTargetUp Smax 已满时新 target 的 up 序列也无法创建。
func TestSeriesLimitBlocksNewTargetUp(t *testing.T) {
	h, q, s := setup(t, 1000, 0, 1, 5)
	if err := h.Append("x", 1, 1); err != nil {
		t.Fatal(err)
	}
	_, err := s.Scrape(100, "nt", okFetch(map[string]int64{"a": 1}))
	if err != head.ErrTooManySeries {
		t.Fatalf("期望 ErrTooManySeries，得到 %v", err)
	}
	wantInstant(t, q, "nt/up", 1000, query.Result{Kind: query.Absent})
	if _, ok := h.Snapshot()["nt/up"]; ok {
		t.Fatal("失败抓取不应写入 nt/up")
	}
	t.Logf("判定依据: 新序列按集合内累计计入 Smax；预校验失败整批不写，up 序列不存在")
}
