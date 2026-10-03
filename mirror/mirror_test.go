package mirror_test

import (
	"errors"
	"testing"

	"ontology/diff"
	"ontology/mirror"
)

func setOf(keys ...string) map[string]struct{} {
	s := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		s[k] = struct{}{}
	}
	return s
}

func baseCfg() mirror.Config {
	return mirror.Config{
		K: 2, Cm: 1, Bm: 100, E: 2, P: 100,
		Sensitive: setOf("authorization", "cookie"),
		Ignore:    setOf("date"),
	}
}

func resp(status int, fields map[string]string) diff.Response {
	return diff.Response{Status: status, Fields: fields}
}

func ptrResp(r diff.Response) *diff.Response { return &r }

func must(t *testing.T, got, want error, ctx string) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("%s: err=%v 想要 %v", ctx, got, want)
	}
}

func mustSkip(t *testing.T, sk *mirror.Skip, reason int, basis string) {
	t.Helper()
	if sk == nil || sk.Reason != reason {
		t.Fatalf("期望跳过原因 %d(%s), 得 %+v", reason, basis, sk)
	}
	t.Logf("跳过: reason=%d; 依据: %s", reason, basis)
}

func mustDispatch(t *testing.T, d *mirror.Dispatch, sk *mirror.Skip, id int64, basis string) {
	t.Helper()
	if sk != nil || d == nil || d.MirrorID != id {
		t.Fatalf("期望分派 #%d(%s), 得 d=%+v sk=%+v", id, basis, d, sk)
	}
	t.Logf("分派: mirrorID=%d headers=%v; 依据: %s", d.MirrorID, d.Headers, basis)
}

func TestSamplingAndBusy(t *testing.T) {
	m, err := mirror.New(baseCfg())
	must(t, err, nil, "构造 k=2 Cm=1")
	// c=1 未采中；c=2 分派#1；c=3 未采中；c=4 采中但在途1≥Cm → 繁忙，不补发。
	_, sk, e := m.Mirror("r1", "GET", 0, nil, 1)
	must(t, e, nil, "r1")
	mustSkip(t, sk, mirror.SkipNotSelected, "c=1 mod 2 != 0")
	d, sk, e := m.Mirror("r2", "GET", 0, nil, 2)
	must(t, e, nil, "r2")
	mustDispatch(t, d, sk, 1, "c=2 命中采样，在途 0→1")
	_, sk, e = m.Mirror("r3", "GET", 0, nil, 3)
	must(t, e, nil, "r3")
	mustSkip(t, sk, mirror.SkipNotSelected, "c=3 未采中")
	_, sk, e = m.Mirror("r4", "GET", 0, nil, 4)
	must(t, e, nil, "r4")
	mustSkip(t, sk, mirror.SkipBusy, "c=4 命中但在途 1≥Cm=1，繁忙丢弃不补发")
	st := m.Stats()
	if st.Accepted != 4 || st.Dispatched != 1 || st.InFlight != 1 ||
		st.SkippedSample != 2 || st.SkippedBusy != 1 {
		t.Fatalf("stats 不符: %+v", st)
	}
	t.Logf("输入: 四次 GET@1..4 => stats=%+v; 依据: 采中样本繁忙即丢不补发，c 仍已推进", st)
}

func TestFilteredDoNotAdvanceC(t *testing.T) {
	m, _ := mirror.New(baseCfg())
	_, sk, _ := m.Mirror("p1", "POST", 0, nil, 1)
	mustSkip(t, sk, mirror.SkipUnsafe, "AU=false 非安全方法，不推进 c")
	_, sk, _ = m.Mirror("p2", "GET", 101, nil, 2)
	mustSkip(t, sk, mirror.SkipBodyTooLarge, "bodyLen 101 > Bm 100，不推进 c")
	_, sk, _ = m.Mirror("g1", "GET", 0, nil, 3)
	mustSkip(t, sk, mirror.SkipNotSelected, "c=1（被过滤请求未推进计数）")
	d, sk, _ := m.Mirror("g2", "GET", 0, nil, 4)
	mustDispatch(t, d, sk, 1, "c=2 分派，证明前两次跳过未推进 c")

	cfg := baseCfg()
	cfg.AllowUnsafe = true
	m2, _ := mirror.New(cfg)
	_, sk, _ = m2.Mirror("p1", "POST", 0, nil, 1)
	mustSkip(t, sk, mirror.SkipNotSelected, "AU=true 时 POST 合格，c=1 未采中")

	m3, _ := mirror.New(baseCfg())
	_, sk, _ = m3.Mirror("e", "GET", 100, nil, 1)
	mustSkip(t, sk, mirror.SkipNotSelected, "bodyLen==Bm=100 恰等于上限放行（c=1）")
}

func TestPauseSemantics(t *testing.T) {
	m, _ := mirror.New(baseCfg()) // E=2 P=100
	_, sk, _ := m.Mirror("w", "GET", 0, nil, 1)
	mustSkip(t, sk, mirror.SkipNotSelected, "预热 c=1 未采中")
	d, _, _ := m.Mirror("a", "GET", 0, nil, 2)
	mustDispatch(t, d, nil, 1, "c=2 分派 #1")
	must(t, m.Done(1, nil, errors.New("boom"), 10), nil, "#1 错误 s=1，在途归零")
	_, sk, _ = m.Mirror("m3", "GET", 0, nil, 11)
	mustSkip(t, sk, mirror.SkipNotSelected, "c=3 未采中")
	d, _, _ = m.Mirror("b", "GET", 0, nil, 12)
	mustDispatch(t, d, nil, 2, "c=4 分派 #2")
	must(t, m.Done(2, nil, errors.New("boom"), 20), nil, "第二次错误 s=2>=E → pausedUntil=120, s=0")
	if pu := m.Stats().PausedUntil; pu != 120 {
		t.Fatalf("pausedUntil=%d 想要 120", pu)
	}
	_, sk, _ = m.Mirror("c", "GET", 0, nil, 119)
	mustSkip(t, sk, mirror.SkipPaused, "now=119<120 暂停，不推进 c")
	d, sk, e := m.Mirror("d", "GET", 0, nil, 120)
	must(t, e, nil, "now==pausedUntil 边界恢复")
	mustSkip(t, sk, mirror.SkipNotSelected, "恢复后 c=5（暂停请求未推进 c）")
	d, sk, e = m.Mirror("e", "GET", 0, nil, 121)
	must(t, e, nil, "")
	mustDispatch(t, d, sk, 3, "c=6 命中，分派 #3")
	must(t, m.Done(3, nil, errors.New("x"), 122), nil, "恢复后首次错误 s=1，不触发暂停（证明暂停期未累计）")
	if m.Stats().PausedUntil != 120 {
		t.Fatal("单个错误不应再次触发暂停")
	}
}

func TestSuccessResetsStreak(t *testing.T) {
	m, _ := mirror.New(baseCfg())
	_, sk, _ := m.Mirror("w", "GET", 0, nil, 1)
	mustSkip(t, sk, mirror.SkipNotSelected, "预热 c=1")
	d, _, _ := m.Mirror("a", "GET", 0, nil, 2)
	mustDispatch(t, d, nil, 1, "")
	must(t, m.Done(1, nil, errors.New("e"), 10), nil, "s=1")
	_, sk, _ = m.Mirror("m3", "GET", 0, nil, 11)
	mustSkip(t, sk, mirror.SkipNotSelected, "c=3")
	d, _, _ = m.Mirror("b", "GET", 0, nil, 12)
	mustDispatch(t, d, nil, 2, "c=4 分派 #2")
	must(t, m.Done(2, ptrResp(resp(200, nil)), nil, 20), nil, "成功清零 s，不触发暂停")
	if m.Stats().PausedUntil != 0 {
		t.Fatal("成功应清零连续失败")
	}
}

func TestClassifyCases(t *testing.T) {
	cases := []struct {
		name  string
		p     diff.Response
		s     diff.Response
		want  diff.Category
		basis string
	}{
		{"兼容", resp(200, map[string]string{"a": "1", "date": "x"}),
			resp(200, map[string]string{"a": "1", "b": "2", "date": "y"}), diff.Compatible,
			"剔除 date 后镜像仅多出 b，主字段一致"},
		{"破坏-值不同", resp(200, map[string]string{"a": "2"}), resp(200, map[string]string{"a": "1"}),
			diff.Breaking, "主字段 a 值不同"},
		{"破坏-缺失", resp(200, map[string]string{"a": "1"}), resp(200, nil),
			diff.Breaking, "镜像缺少主字段 a"},
		{"破坏-状态码", resp(200, map[string]string{"a": "1"}), resp(500, map[string]string{"a": "1"}),
			diff.Breaking, "状态码 200 != 500"},
		{"相同-忽略日期", resp(200, map[string]string{"a": "1", "date": "x"}),
			resp(200, map[string]string{"a": "1", "date": "y"}), diff.Identical, "date 在 IG 中"},
		{"相同", resp(200, map[string]string{"a": "1"}), resp(200, map[string]string{"a": "1"}),
			diff.Identical, "状态码与字段全一致"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := diff.Classify(tc.p, tc.s, baseCfg().Ignore)
			if got != tc.want {
				t.Fatalf("Classify=%s 想要 %s", got, tc.want)
			}
			t.Logf("输入: primary=%v shadow=%v => 输出: %s; 依据: %s", tc.p, tc.s, got, tc.basis)
		})
	}
}

func TestPairingOrder(t *testing.T) {
	for _, doneFirst := range []bool{true, false} {
		m, _ := mirror.New(baseCfg())
		_, sk, _ := m.Mirror("w", "GET", 0, nil, 1)
		mustSkip(t, sk, mirror.SkipNotSelected, "预热 c=1")
		d, _, _ := m.Mirror("a", "GET", 0, nil, 2)
		mustDispatch(t, d, nil, 1, "c=2 分派")
		pr := resp(200, map[string]string{"a": "1"})
		sr := resp(200, map[string]string{"a": "1", "b": "2"})
		if doneFirst {
			must(t, m.Done(1, ptrResp(sr), nil, 5), nil, "成功 Done 先到挂起")
			if m.Stats().PendingPrimary != 1 {
				t.Fatal("应计入 PendingPrimary")
			}
			must(t, m.Primary("a", pr), nil, "Primary 到齐才分类")
		} else {
			must(t, m.Primary("a", pr), nil, "Primary 先到挂起")
			if m.Stats().PendingDone != 1 {
				t.Fatal("应计入 PendingDone")
			}
			must(t, m.Done(1, ptrResp(sr), nil, 5), nil, "Done 到齐才分类")
		}
		st := m.Stats()
		if st.Compatible != 1 || st.PendingPrimary != 0 || st.PendingDone != 0 {
			t.Fatalf("doneFirst=%v stats=%+v", doneFirst, st)
		}
		t.Logf("doneFirst=%v => 同一对响应分类一致(兼容)，半到计数已冲销 stats=%+v", doneFirst, st)
	}
}

func TestErrorDoneNeverClassifies(t *testing.T) {
	// Primary 先到、错误 Done 后到：不分类，半到计数冲销。
	m, _ := mirror.New(baseCfg())
	_, sk, _ := m.Mirror("w", "GET", 0, nil, 1)
	mustSkip(t, sk, mirror.SkipNotSelected, "预热")
	d, _, _ := m.Mirror("a", "GET", 0, nil, 2)
	mustDispatch(t, d, nil, 1, "")
	must(t, m.Primary("a", resp(200, map[string]string{"a": "1"})), nil, "Primary 先到")
	if m.Stats().PendingDone != 1 {
		t.Fatal("应有一个 PendingDone")
	}
	must(t, m.Done(1, nil, errors.New("net"), 5), nil, "错误 Done：不分类，冲销半到计数")
	st := m.Stats()
	if st.Identical+st.Compatible+st.Breaking != 0 || st.PendingDone != 0 || st.ShadowErrors != 1 {
		t.Fatalf("错误镜像不得分类: %+v", st)
	}
	// 错误 Done 之后再到 Primary 报重复。
	if err := m.Primary("a", resp(200, nil)); !errors.Is(err, mirror.ErrDuplicate) {
		t.Fatalf("重复 Primary 应报 ErrDuplicate, 得 %v", err)
	}
}

func TestHeaderStripAndShadowOverride(t *testing.T) {
	m, _ := mirror.New(baseCfg())
	hs := map[string]string{
		"Authorization": "tok", "COOKIE": "c=1", "X-Trace": "t", "X-Shadow": "old",
	}
	_, _, _ = m.Mirror("w", "GET", 0, nil, 1)
	d, _, _ := m.Mirror("a", "GET", 0, hs, 2)
	mustDispatch(t, d, nil, 1, "")
	if _, ok := d.Headers["authorization"]; ok {
		t.Fatal("Authorization 应被剥离")
	}
	if _, ok := d.Headers["cookie"]; ok {
		t.Fatal("Cookie 应被剥离")
	}
	if d.Headers["x-trace"] != "t" {
		t.Fatal("普通头应保留并统一小写")
	}
	if d.Headers["x-shadow"] != "1" {
		t.Fatal("原有 x-shadow 应被覆盖为 1")
	}
	// 输入头映射不应被修改。
	if hs["X-Shadow"] != "old" {
		t.Fatal("不得修改调用方传入的 headers")
	}
	// 大小写不同的同名头非法。
	_, _, err := m.Mirror("b", "GET", 0, map[string]string{"Foo": "1", "foo": "2"}, 3)
	if !errors.Is(err, mirror.ErrInvalid) {
		t.Fatalf("大小写同名头应报 ErrInvalid, 得 %v", err)
	}
	// 空头名非法。
	if _, _, err := m.Mirror("c", "GET", 0, map[string]string{"": "v"}, 4); !errors.Is(err, mirror.ErrInvalid) {
		t.Fatalf("空头名应非法, 得 %v", err)
	}
	t.Logf("输入: %v => 镜像头: %v; 依据: 敏感头小写匹配剥离，x-shadow 覆盖，输出为副本", hs, d.Headers)
}

func TestRejectionsAndOrder(t *testing.T) {
	m, _ := mirror.New(baseCfg())
	// 参数非法优先于时间非法。
	if _, _, err := m.Mirror("", "GET", 0, nil, -1); !errors.Is(err, mirror.ErrInvalid) {
		t.Fatalf("空 reqID 应 ErrInvalid, 得 %v", err)
	}
	if _, _, err := m.Mirror("x", "OPTIONS", 0, nil, -1); !errors.Is(err, mirror.ErrInvalid) {
		t.Fatalf("非法 method 应 ErrInvalid, 得 %v", err)
	}
	if _, _, err := m.Mirror("x", "GET", -1, nil, -1); !errors.Is(err, mirror.ErrInvalid) {
		t.Fatalf("负 bodyLen 应 ErrInvalid, 得 %v", err)
	}
	// 时间非法先于时钟回退与重复：即使 reqID 已分派，now 超界仍只报时间非法。
	if _, _, err := m.Mirror("x", "GET", 0, nil, 1<<60); !errors.Is(err, mirror.ErrTime) {
		t.Fatalf("now 超界应 ErrTime(先于重复/回退), 得 %v", err)
	}
	before := m.Stats()
	// 建立已分派 reqID：c=1 未采中（w），c=2 分派（x）。
	_, sk, _ := m.Mirror("w", "GET", 0, nil, 5)
	mustSkip(t, sk, mirror.SkipNotSelected, "c=1 未采中")
	d, _, _ := m.Mirror("x", "GET", 0, nil, 6)
	mustDispatch(t, d, nil, 1, "c=2 分派 x")
	// 时钟回退先于重复判定。
	if _, _, err := m.Mirror("x", "GET", 0, nil, 5); !errors.Is(err, mirror.ErrClock) {
		t.Fatalf("回退应 ErrClock(先于重复), 得 %v", err)
	}
	// 时钟通过后已分派 reqID 报重复。
	if _, _, err := m.Mirror("x", "GET", 0, nil, 7); !errors.Is(err, mirror.ErrDuplicate) {
		t.Fatalf("重复应 ErrDuplicate, 得 %v", err)
	}
	// 被跳过的 reqID 可再次提交（只有已分派者报重复）。
	_, sk, _ = m.Mirror("y", "GET", 0, nil, 8)
	mustSkip(t, sk, mirror.SkipNotSelected, "y c=3 未采中（被跳过不占编号）")
	if _, sk2, err := m.Mirror("y", "GET", 0, nil, 9); err != nil {
		t.Fatalf("被跳过的 reqID 应可再次提交, 得 err=%v sk=%+v", err, sk2)
	} else {
		t.Logf("y 再次提交不报重复（此前仅被跳过）: sk=%+v", sk2)
	}
	// 此前所有被拒绝操作（含 x 的回退/重复）不得改变任何统计。
	after := m.Stats()
	if after.Accepted != before.Accepted+4 { // w,x,y,y 四次通过检查
		t.Fatalf("拒绝不得改变 Accepted: before=%d after=%d", before.Accepted, after.Accepted)
	}
	// Primary：参数非法 > 未知 > 重复。
	if err := m.Primary("", resp(200, nil)); !errors.Is(err, mirror.ErrInvalid) {
		t.Fatalf("Primary 空 id 应 ErrInvalid, 得 %v", err)
	}
	if err := m.Primary("nope", resp(200, nil)); !errors.Is(err, mirror.ErrUnknown) {
		t.Fatalf("未知 Primary 应 ErrUnknown, 得 %v", err)
	}
	must(t, m.Primary("x", resp(200, nil)), nil, "首次 Primary x 成功（#1 尚未 Done，挂起）")
	if err := m.Primary("x", resp(200, nil)); !errors.Is(err, mirror.ErrDuplicate) {
		t.Fatalf("重复 Primary 应 ErrDuplicate, 得 %v", err)
	}
	// Done：未知 > 重复；编号非法先报参数非法。
	if err := m.Done(0, ptrResp(resp(200, nil)), nil, 9); !errors.Is(err, mirror.ErrInvalid) {
		t.Fatalf("Done id=0 应 ErrInvalid, 得 %v", err)
	}
	if err := m.Done(1, nil, nil, 9); !errors.Is(err, mirror.ErrInvalid) {
		t.Fatalf("resp 与 error 同时缺失应 ErrInvalid, 得 %v", err)
	}
	if err := m.Done(99, ptrResp(resp(200, nil)), nil, 9); !errors.Is(err, mirror.ErrUnknown) {
		t.Fatalf("未知 Done 应 ErrUnknown, 得 %v", err)
	}
	if err := m.Done(99, nil, errors.New("e"), 9); !errors.Is(err, mirror.ErrUnknown) {
		t.Fatalf("错误响应未知 Done 仍应 ErrUnknown, 得 %v", err)
	}
	if err := m.Done(1, nil, errors.New("e"), 8); !errors.Is(err, mirror.ErrClock) {
		t.Fatalf("Done 时钟回退应 ErrClock(先于状态变更), 得 %v", err)
	}
	must(t, m.Done(1, nil, errors.New("e"), 9), nil, "Done #1 错误，在途归零")
	if err := m.Done(1, ptrResp(resp(200, nil)), nil, 10); !errors.Is(err, mirror.ErrDuplicate) {
		t.Fatalf("重复 Done 应 ErrDuplicate, 得 %v", err)
	}
}

func TestInvalidConfigs(t *testing.T) {
	good := baseCfg()
	for _, mutate := range []func(*mirror.Config){
		func(c *mirror.Config) { c.K = 0 },
		func(c *mirror.Config) { c.K = 1_000_001 },
		func(c *mirror.Config) { c.Cm = 0 },
		func(c *mirror.Config) { c.Bm = -1 },
		func(c *mirror.Config) { c.Bm = 1_000_000_001 },
		func(c *mirror.Config) { c.E = 0 },
		func(c *mirror.Config) { c.P = 0 },
		func(c *mirror.Config) { c.P = 1_000_000_001 },
	} {
		cfg := good
		mutate(&cfg)
		if _, err := mirror.New(cfg); !errors.Is(err, mirror.ErrInvalid) {
			t.Fatalf("越界配置应 ErrInvalid: %+v 得 %v", cfg, err)
		}
	}
}
