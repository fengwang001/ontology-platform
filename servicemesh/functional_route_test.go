package servicemesh

import (
	"testing"
	"time"
)

func TestBucketBoundariesAndZeroWeight(t *testing.T) {
	l := newLogger(t)
	m := NewMesh()
	cfg := &ServiceConfig{
		Rules: []Rule{{
			Matches: []MatchItem{{Path: PathMatch{Kind: PathExact, Path: "/"}}},
			Targets: []Target{
				{Subset: "z", Weight: 0},
				{Subset: "a", Weight: 30},
				{Subset: "b", Weight: 70},
			},
		}},
	}
	mustPublish(t, l, m, "svc", cfg, 0)
	if e := m.RegisterSubsets("svc", allReadySubsets("z", "a", "b")); e != nil {
		t.Fatal(e)
	}
	// 排布：z 空；a=[0,2999]；b=[3000,9999]
	for _, c := range []struct {
		bucket int
		subset string
		basis  string
	}{
		{0, "a", "零权重 z 不承接任何分桶，首个分桶归 a"},
		{2999, "a", "a 恰好承接 30*100 个相邻分桶 [0,2999]"},
		{3000, "b", "边界 3000 归 b"},
		{9999, "b", "最后一个分桶归 b"},
	} {
		req := Request{Path: "/", Bucket: c.bucket}
		res, e := m.Route("svc", req)
		if e != nil {
			t.Fatal(e)
		}
		l.route("svc", req, res, nil, c.basis)
		if res.Subset != c.subset {
			t.Fatalf("bucket %d got %s want %s", c.bucket, res.Subset, c.subset)
		}
	}

	// 全量枚举每个分桶，严格校验区间。
	counts := map[string]int{}
	for b := 0; b < 10000; b++ {
		res, e := m.Route("svc", Request{Path: "/", Bucket: b})
		if e != nil {
			t.Fatal(e)
		}
		counts[res.Subset]++
	}
	if counts["z"] != 0 || counts["a"] != 3000 || counts["b"] != 7000 {
		t.Fatalf("分桶计数错误: %v", counts)
	}
	l.log("全量枚举 10000 个分桶 => counts=%v | 判定依据: z=0,a=3000,b=7000", counts)
}

func TestPolicyFieldwiseInheritance(t *testing.T) {
	l := newLogger(t)
	m := NewMesh()
	cfg := &ServiceConfig{
		Default: Policy{
			Timeout:           dur(1000 * time.Millisecond),
			PerAttemptTimeout: dur(200 * time.Millisecond),
		},
		Rules: []Rule{
			{
				Matches:  []MatchItem{{Path: PathMatch{Kind: PathExact, Path: "/override"}}},
				Targets:  []Target{{Subset: "s1", Weight: 100}},
				Override: &Policy{Timeout: dur(500 * time.Millisecond), MaxRetries: intPtr(2)},
			},
			{
				Matches: []MatchItem{{Path: PathMatch{Kind: PathExact, Path: "/inherit"}}},
				Targets: []Target{{Subset: "s2", Weight: 100}},
			},
		},
		Fallbacks: []Target{{Subset: "fb", Weight: 100}},
	}
	mustPublish(t, l, m, "svc", cfg, 0)
	if e := m.RegisterSubsets("svc", allReadySubsets("s1", "s2", "fb")); e != nil {
		t.Fatal(e)
	}

	res, e := m.Route("svc", Request{Path: "/override", Bucket: 0})
	if e != nil {
		t.Fatal(e)
	}
	l.route("svc", Request{Path: "/override"}, res, nil, "timeout/retries 取覆盖值，perAttempt 继承默认")
	if *res.Policy.Timeout != 500*time.Millisecond ||
		*res.Policy.PerAttemptTimeout != 200*time.Millisecond ||
		*res.Policy.MaxRetries != 2 {
		t.Fatalf("覆盖策略错误: %+v", res.Policy)
	}

	res, e = m.Route("svc", Request{Path: "/inherit", Bucket: 0})
	if e != nil {
		t.Fatal(e)
	}
	l.route("svc", Request{Path: "/inherit"}, res, nil, "无覆盖：继承默认，retries 未设置")
	if *res.Policy.Timeout != 1000*time.Millisecond ||
		*res.Policy.PerAttemptTimeout != 200*time.Millisecond ||
		res.Policy.MaxRetries != nil {
		t.Fatalf("继承策略错误: %+v", res.Policy)
	}

	res, e = m.Route("svc", Request{Path: "/nope", Bucket: 0})
	if e != nil {
		t.Fatal(e)
	}
	l.route("svc", Request{Path: "/nope"}, res, nil, "兜底使用服务默认策略")
	if *res.Policy.Timeout != 1000*time.Millisecond || res.Policy.MaxRetries != nil {
		t.Fatalf("兜底策略错误: %+v", res.Policy)
	}

	// 返回策略被调用方修改不影响内部状态。
	*res.Policy.Timeout = 42 * time.Nanosecond
	res2, _ := m.Route("svc", Request{Path: "/nope", Bucket: 0})
	if *res2.Policy.Timeout != 1000*time.Millisecond {
		t.Fatal("RouteResult.Policy 与内部状态共享了内存")
	}
}

func TestSubsetMissingAndNoReadyEndpoint(t *testing.T) {
	l := newLogger(t)
	m := NewMesh()
	cfg := &ServiceConfig{
		Rules: []Rule{{
			Matches: []MatchItem{{Path: PathMatch{Kind: PathExact, Path: "/"}}},
			Targets: []Target{{Subset: "good", Weight: 50}, {Subset: "bad", Weight: 50}},
		}},
	}
	mustPublish(t, l, m, "svc", cfg, 0)
	if e := m.RegisterSubsets("svc", map[string][]Endpoint{
		"good": {
			{Name: "g1", Ready: true},
			{Name: "g2", Ready: true},
		},
		"bad": {
			{Name: "b1", Ready: false}, // 全部未就绪
		},
	}); e != nil {
		t.Fatal(e)
	}

	res, e := m.Route("svc", Request{Path: "/", Bucket: 0})
	if e != nil {
		t.Fatal(e)
	}
	l.route("svc", Request{Path: "/", Bucket: 0}, res, nil, "good 子集就绪端点轮询")
	if res.Endpoint != "g1" {
		t.Fatalf("首个端点应为 g1, got %s", res.Endpoint)
	}

	// 轮询在就绪端点间进行。
	names := map[string]int{}
	for i := 0; i < 4; i++ {
		r, _ := m.Route("svc", Request{Path: "/", Bucket: 0})
		names[r.Endpoint]++
	}
	if names["g1"] != 2 || names["g2"] != 2 {
		t.Fatalf("就绪端点轮询错误: %v", names)
	}
	l.log("4 次同子集请求端点分布=%v | 判定依据: 仅在 2 个就绪端点间轮询", names)

	// 未登记子集：无可用端点，不改道。
	_, e = m.Route("svc", Request{Path: "/", Bucket: 5000})
	if e == nil || e.Class != ClassNoEndpoint {
		t.Fatalf("期望无可用端点, got %v", e)
	}
	l.route("svc", Request{Path: "/", Bucket: 5000}, nil, e, "bad 子集无就绪端点：无可用端点，不改道")

	// 未注册过的子集名同样报无可用端点。
	m2 := NewMesh()
	mustPublish(t, l, m2, "svc2", &ServiceConfig{Rules: []Rule{{
		Matches: []MatchItem{{Path: PathMatch{Kind: PathExact, Path: "/"}}},
		Targets: []Target{{Subset: "ghost", Weight: 100}},
	}}}, 0)
	_, e = m2.Route("svc2", Request{Path: "/", Bucket: 0})
	if e == nil || e.Class != ClassNoEndpoint {
		t.Fatalf("未登记子集期望无可用端点, got %v", e)
	}
	l.route("svc2", Request{Path: "/"}, nil, e, "子集完全未登记：无可用端点")
}

func TestInvalidArguments(t *testing.T) {
	l := newLogger(t)
	m := NewMesh()
	cases := []struct {
		name string
		cfg  *ServiceConfig
	}{
		{"nil配置", nil},
		{"规则无匹配项", &ServiceConfig{Rules: []Rule{{Targets: []Target{{Subset: "s", Weight: 100}}}}}},
		{"规则无目标", &ServiceConfig{Rules: []Rule{{
			Matches: []MatchItem{{Path: PathMatch{Kind: PathExact, Path: "/"}}},
		}}}},
		{"路径不以斜杠开头", &ServiceConfig{Rules: []Rule{{
			Matches: []MatchItem{{Path: PathMatch{Kind: PathExact, Path: "x"}}},
			Targets: []Target{{Subset: "s", Weight: 100}},
		}}}},
		{"路径含空段", &ServiceConfig{Rules: []Rule{{
			Matches: []MatchItem{{Path: PathMatch{Kind: PathPrefix, Path: "/a//b"}}},
			Targets: []Target{{Subset: "s", Weight: 100}},
		}}}},
		{"空头名", &ServiceConfig{Rules: []Rule{{
			Matches: []MatchItem{{Path: PathMatch{Kind: PathExact, Path: "/"},
				Headers: []HeaderMatch{{Name: "  ", Op: HeaderPresent}}}},
			Targets: []Target{{Subset: "s", Weight: 100}},
		}}}},
		{"负超时", &ServiceConfig{
			Default: Policy{Timeout: dur(-time.Second)},
			Rules: []Rule{{
				Matches: []MatchItem{{Path: PathMatch{Kind: PathExact, Path: "/"}}},
				Targets: []Target{{Subset: "s", Weight: 100}},
			}},
		}},
	}
	for _, c := range cases {
		_, e := m.Publish("svc", c.cfg, 0)
		l.publish("svc", c.cfg, 0, 0, e, ClassInvalidArgument)
		if e == nil || e.Class != ClassInvalidArgument {
			t.Fatalf("%s: 期望参数非法, got %v", c.name, e)
		}
	}

	// 请求参数非法。
	if _, e := m.Route("svc", Request{Path: "/", Bucket: 10000}); e == nil || e.Class != ClassInvalidArgument {
		t.Fatalf("越界分桶期望参数非法, got %v", e)
	}
	_, badBucketErr := m.Route("svc", Request{Path: "/", Bucket: 10000})
	l.log("ROUTE bucket=10000 => err=%v | 判定依据: 分桶越界", errText(badBucketErr))
	if _, e := m.Route("svc", Request{Path: "/", Bucket: -1}); e == nil || e.Class != ClassInvalidArgument {
		t.Fatalf("负分桶期望参数非法, got %v", e)
	}
	if _, e := m.Publish("", &ServiceConfig{}, 0); e == nil || e.Class != ClassInvalidArgument {
		t.Fatalf("空服务名期望参数非法, got %v", e)
	}
	if _, e := m.Route("svc", Request{Path: "relative/x", Bucket: 0}); e == nil || e.Class != ClassInvalidArgument {
		t.Fatalf("非斜杠开头请求路径期望参数非法, got %v", e)
	}
	// 参数非法优先级高于无路由：未知服务 + 越界分桶仍报参数非法。
	if _, e := m.Route("unknown", Request{Path: "/", Bucket: 10000}); e == nil || e.Class != ClassInvalidArgument {
		t.Fatalf("参数非法应优先于无路由, got %v", e)
	}
	l.log("参数错误优先级检查完成（未知服务+越界分桶 => 参数非法）")
}

func TestRegisterValidationAndUnpublishedService(t *testing.T) {
	l := newLogger(t)
	m := NewMesh()

	if e := m.RegisterSubsets("", map[string][]Endpoint{"s": {{Name: "e", Ready: true}}}); e == nil ||
		e.Class != ClassInvalidArgument {
		t.Fatalf("空服务名注册应参数非法, got %v", e)
	}
	if e := m.RegisterSubsets("svc", map[string][]Endpoint{"": {{Name: "e", Ready: true}}}); e == nil ||
		e.Class != ClassInvalidArgument {
		t.Fatalf("空子集名应参数非法, got %v", e)
	}
	if e := m.RegisterSubsets("svc", map[string][]Endpoint{"s": {{Name: "", Ready: true}}}); e == nil ||
		e.Class != ClassInvalidArgument {
		t.Fatalf("空端点名应参数非法, got %v", e)
	}
	l.log("注册参数非法检查全部通过")

	// 注册失败不得改变既有子集状态。
	if e := m.RegisterSubsets("svc", map[string][]Endpoint{
		"good": {{Name: "g1", Ready: true}},
	}); e != nil {
		t.Fatal(e)
	}
	if e := m.RegisterSubsets("svc", map[string][]Endpoint{
		"good": {{Name: "g1", Ready: true}},
		"bad":  {{Name: "", Ready: true}},
	}); e == nil || e.Class != ClassInvalidArgument {
		t.Fatalf("整份注册应原子失败, got %v", e)
	}

	// 只注册子集但从未发布配置：无路由（而非其他错误）。
	_, e := m.Route("svc", Request{Path: "/", Bucket: 0})
	if e == nil || e.Class != ClassNoRoute {
		t.Fatalf("未发布服务应无路由, got %v", e)
	}
	if v := m.Version("svc"); v != 0 {
		t.Fatalf("未发布版本应为0, got %d", v)
	}
	if _, ver, ok := m.GetConfig("svc"); ok || ver != 0 {
		t.Fatalf("未发布 GetConfig 应 ok=false ver=0, got ok=%v ver=%d", ok, ver)
	}
	l.route("svc", Request{Path: "/"}, nil, e, "只注册子集未发布配置 => 无路由，版本0")
}
