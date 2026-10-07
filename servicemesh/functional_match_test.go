package servicemesh

import "testing"

func mustPublish(t *testing.T, l *opLogger, m *Mesh, service string, cfg *ServiceConfig, base uint64) uint64 {
	t.Helper()
	v, e := m.Publish(service, cfg, base)
	l.publish(service, cfg, base, v, e, -1)
	if e != nil {
		t.Fatalf("publish: %v", e)
	}
	return v
}

func allReadySubsets(names ...string) map[string][]Endpoint {
	out := map[string][]Endpoint{}
	for _, n := range names {
		out[n] = []Endpoint{{Name: n + "-ep", Ready: true}}
	}
	return out
}

func TestPathSegmentBoundaryAndQuery(t *testing.T) {
	l := newLogger(t)
	m := NewMesh()
	cfg := &ServiceConfig{
		Rules: []Rule{
			{
				Matches: []MatchItem{{Path: PathMatch{Kind: PathPrefix, Path: "/api/v1"}}},
				Targets: []Target{{Subset: "a", Weight: 30}, {Subset: "b", Weight: 70}},
			},
			{
				Matches: []MatchItem{{Path: PathMatch{Kind: PathExact, Path: "/exact"}}},
				Targets: []Target{{Subset: "x", Weight: 100}},
			},
		},
		Fallbacks: []Target{{Subset: "fb", Weight: 100}},
	}
	mustPublish(t, l, m, "svc", cfg, 0)
	if e := m.RegisterSubsets("svc", allReadySubsets("a", "b", "x", "fb")); e != nil {
		t.Fatal(e)
	}

	cases := []struct {
		path   string
		rule   int
		subset string
		basis  string
	}{
		{"/api/v1", 0, "a", "前缀等于整条路径"},
		{"/api/v1/users", 0, "a", "段边界前缀，后面跟 /"},
		{"/api/v12", -1, "fb", "半个段不匹配前缀，且不等于精确路径，走兜底"},
		{"/api/v", -1, "fb", "不是段边界前缀，走兜底"},
		{"/api/v1?x=1&y=2", 0, "a", "路径匹配前先去掉查询串"},
		{"/exact?z=", 1, "x", "精确匹配 + 去查询串"},
		{"/exact/2", -1, "fb", "精确条件不匹配多一段的路径"},
		{"/", -1, "fb", "根路径不命中任何规则，走兜底"},
	}
	for _, c := range cases {
		req := Request{Path: c.path, Bucket: 0}
		res, e := m.Route("svc", req)
		if e != nil {
			t.Fatalf("path %s: %v", c.path, e)
		}
		l.route("svc", req, res, nil, c.basis)
		if res.RuleIdx != c.rule || res.Subset != c.subset {
			t.Fatalf("path %s got rule=%d subset=%s want rule=%d subset=%s",
				c.path, res.RuleIdx, res.Subset, c.rule, c.subset)
		}
	}
}

func TestHeaderConditionsAndMultiValue(t *testing.T) {
	l := newLogger(t)
	m := NewMesh()
	cfg := &ServiceConfig{
		Rules: []Rule{
			{Matches: []MatchItem{{
				Path:    PathMatch{Kind: PathExact, Path: "/"},
				Headers: []HeaderMatch{{Name: "x-env", Op: HeaderExact, Value: "prod"}},
			}}, Targets: []Target{{Subset: "exact", Weight: 100}}},
			{Matches: []MatchItem{{
				Path:    PathMatch{Kind: PathExact, Path: "/"},
				Headers: []HeaderMatch{{Name: "x-tenant", Op: HeaderPrefix, Value: "team-"}},
			}}, Targets: []Target{{Subset: "prefix", Weight: 100}}},
			{Matches: []MatchItem{{
				Path:    PathMatch{Kind: PathExact, Path: "/"},
				Headers: []HeaderMatch{{Name: "X-Debug", Op: HeaderPresent}},
			}}, Targets: []Target{{Subset: "present", Weight: 100}}},
		},
	}
	mustPublish(t, l, m, "svc", cfg, 0)
	if e := m.RegisterSubsets("svc", allReadySubsets("exact", "prefix", "present")); e != nil {
		t.Fatal(e)
	}

	cases := []struct {
		name    string
		headers map[string][]string
		subset  string // 空表示期望无路由
		basis   string
	}{
		{"exact-头名大小写不敏感", map[string][]string{"X-ENV": {"prod"}}, "exact",
			"精确值相等，头名归一化后比较"},
		{"exact-值大小写敏感", map[string][]string{"x-env": {"Prod"}}, "",
			"值区分大小写，三个规则均不满足"},
		{"多值头任一满足精确", map[string][]string{"x-env": {"dev", "prod"}}, "exact",
			"同名头多值时任一值满足即满足"},
		{"前缀命中", map[string][]string{"x-tenant": {"other", "team-a"}}, "prefix",
			"多值中任一以 team- 开头"},
		{"前缀不命中", map[string][]string{"x-tenant": {"ateam-x"}}, "",
			"不是以 team- 开头"},
		{"存在命中", map[string][]string{"x-debug": {"1"}}, "present", "仅要求存在"},
		{"顺序优先于存在", map[string][]string{"x-env": {"prod"}, "x-debug": {"1"}}, "exact",
			"精确规则声明在前，首个命中胜出"},
		{"无头不命中", map[string][]string{}, "", "所有头条件均不满足"},
	}
	for _, c := range cases {
		req := Request{Path: "/", HeaderValues: c.headers, Bucket: 0}
		res, e := m.Route("svc", req)
		if c.subset == "" {
			if e == nil || e.Class != ClassNoRoute {
				t.Fatalf("%s: 期望无路由, got %v %v", c.name, res, e)
			}
			l.route("svc", req, res, e, c.basis)
			continue
		}
		if e != nil {
			t.Fatalf("%s: %v", c.name, e)
		}
		l.route("svc", req, res, nil, c.basis)
		if res.Subset != c.subset {
			t.Fatalf("%s: got %s want %s", c.name, res.Subset, c.subset)
		}
	}
}

func TestFirstMatchFallbackAndNoRoute(t *testing.T) {
	l := newLogger(t)
	m := NewMesh()
	cfg := &ServiceConfig{
		Rules: []Rule{
			{Matches: []MatchItem{{
				Path:    PathMatch{Kind: PathPrefix, Path: "/a"},
				Headers: []HeaderMatch{{Name: "x-kind", Op: HeaderExact, Value: "first"}},
			}},
				Targets: []Target{{Subset: "r1", Weight: 100}}},
			{Matches: []MatchItem{{Path: PathMatch{Kind: PathPrefix, Path: "/a/b"}}},
				Targets: []Target{{Subset: "r2", Weight: 100}}},
		},
		Fallbacks: []Target{{Subset: "fb", Weight: 100}},
	}
	mustPublish(t, l, m, "svc", cfg, 0)
	if e := m.RegisterSubsets("svc", allReadySubsets("r1", "r2", "fb")); e != nil {
		t.Fatal(e)
	}
	for _, c := range []struct {
		path, subset, basis string
		headers             map[string][]string
		rule                int
	}{
		{"/a/b", "r2", "规则0要求 x-kind=first，缺头不满足；规则1命中",
			nil, 1},
		{"/a/b", "r1", "带上头后规则0在前，第一个命中胜出",
			map[string][]string{"x-kind": {"first"}}, 0},
		{"/c", "fb", "无规则命中走兜底", nil, -1},
	} {
		req := Request{Path: c.path, Bucket: 0, HeaderValues: c.headers}
		res, e := m.Route("svc", req)
		if e != nil {
			t.Fatal(e)
		}
		l.route("svc", req, res, nil, c.basis)
		if res.Subset != c.subset || res.RuleIdx != c.rule {
			t.Fatalf("got %s/%d want %s/%d", res.Subset, res.RuleIdx, c.subset, c.rule)
		}
	}

	// 没有兜底目标时无路由。
	m2 := NewMesh()
	cfg2 := &ServiceConfig{Rules: []Rule{{
		Matches: []MatchItem{{Path: PathMatch{Kind: PathExact, Path: "/x"}}},
		Targets: []Target{{Subset: "s", Weight: 100}},
	}}}
	mustPublish(t, l, m2, "s2", cfg2, 0)
	_, e := m2.Route("s2", Request{Path: "/y", Bucket: 0})
	if e == nil || e.Class != ClassNoRoute {
		t.Fatalf("期望无路由, got %v", e)
	}
	l.log("ROUTE s2 /y => err=%v | 判定依据: 无规则命中且无兜底", errText(e))

	// 从未发布的服务同样无路由（而非参数错误）。
	_, e = m.Route("ghost", Request{Path: "/", Bucket: 0})
	if e == nil || e.Class != ClassNoRoute {
		t.Fatalf("未知服务期望无路由, got %v", e)
	}
}

func TestRootPrefixMatchesEverything(t *testing.T) {
	l := newLogger(t)
	m := NewMesh()
	cfg := &ServiceConfig{
		Rules: []Rule{{
			Matches: []MatchItem{{Path: PathMatch{Kind: PathPrefix, Path: "/"}}},
			Targets: []Target{{Subset: "all", Weight: 100}},
		}},
	}
	mustPublish(t, l, m, "svc", cfg, 0)
	if e := m.RegisterSubsets("svc", allReadySubsets("all")); e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{"/", "/a", "/a/b/c", "/x?y=1"} {
		req := Request{Path: p, Bucket: 0}
		res, e := m.Route("svc", req)
		if e != nil {
			t.Fatal(e)
		}
		l.route("svc", req, res, nil, "根前缀 / 是所有绝对路径的段边界前缀")
		if res.Subset != "all" || res.RuleIdx != 0 {
			t.Fatalf("根前缀未匹配 %s", p)
		}
	}
}

func TestTrailingSlashPathShape(t *testing.T) {
	l := newLogger(t)
	m := NewMesh()
	cfg := &ServiceConfig{Rules: []Rule{{
		Matches: []MatchItem{{Path: PathMatch{Kind: PathPrefix, Path: "/a/"}}},
		Targets: []Target{{Subset: "a", Weight: 100}},
	}}}
	_, e := m.Publish("svc", cfg, 0)
	l.publish("svc", cfg, 0, 0, e, ClassInvalidArgument)
	if e == nil || e.Class != ClassInvalidArgument {
		t.Fatalf("非根尾斜杠路径应参数非法, got %v", e)
	}
}
