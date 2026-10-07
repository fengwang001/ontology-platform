package servicemesh

import "testing"

func twoRuleConfig(r0, r1 MatchItem) *ServiceConfig {
	return &ServiceConfig{Rules: []Rule{
		{Matches: []MatchItem{r0}, Targets: []Target{{Subset: "a", Weight: 100}}},
		{Matches: []MatchItem{r1}, Targets: []Target{{Subset: "b", Weight: 100}}},
	}}
}

func TestShadowEntailment(t *testing.T) {
	l := newLogger(t)
	cases := []struct {
		name   string
		r0     MatchItem
		r1     MatchItem
		reject bool
		basis  string
	}{
		{
			"前缀路径覆盖更长前缀",
			MatchItem{Path: PathMatch{Kind: PathPrefix, Path: "/a"}},
			MatchItem{Path: PathMatch{Kind: PathPrefix, Path: "/a/b"}},
			true, "/a 是 /a/b 的段边界前缀",
		},
		{
			"前缀路径覆盖同路径精确",
			MatchItem{Path: PathMatch{Kind: PathPrefix, Path: "/a"}},
			MatchItem{Path: PathMatch{Kind: PathExact, Path: "/a"}},
			true, "精确 /a 的请求集合是前缀 /a 的子集",
		},
		{
			"半个段不构成覆盖",
			MatchItem{Path: PathMatch{Kind: PathPrefix, Path: "/a"}},
			MatchItem{Path: PathMatch{Kind: PathPrefix, Path: "/ab"}},
			false, "/a 不是 /ab 的段边界前缀",
		},
		{
			"精确只覆盖相同精确",
			MatchItem{Path: PathMatch{Kind: PathExact, Path: "/a"}},
			MatchItem{Path: PathMatch{Kind: PathPrefix, Path: "/a"}},
			false, "精确不覆盖前缀",
		},
		{
			"精确头蕴含前缀头与存在头",
			MatchItem{
				Path:    PathMatch{Kind: PathPrefix, Path: "/a"},
				Headers: []HeaderMatch{{Name: "x", Op: HeaderExact, Value: "team-alpha"}},
			},
			MatchItem{
				Path: PathMatch{Kind: PathPrefix, Path: "/a/b"},
				Headers: []HeaderMatch{
					{Name: "x", Op: HeaderPrefix, Value: "team-"},
					{Name: "x", Op: HeaderPresent},
				},
			},
			true, "精确值蕴含以其开头的前缀与存在；后者多个同名条件任一蕴含即可",
		},
		{
			"前缀头蕴含更短前缀头",
			MatchItem{
				Path:    PathMatch{Kind: PathPrefix, Path: "/a"},
				Headers: []HeaderMatch{{Name: "x", Op: HeaderPrefix, Value: "team-a"}},
			},
			MatchItem{
				Path:    PathMatch{Kind: PathPrefix, Path: "/a/b"},
				Headers: []HeaderMatch{{Name: "x", Op: HeaderPrefix, Value: "team"}},
			},
			true, "team-a 开头的值必以 team 开头",
		},
		{
			"前缀头不蕴含更长前缀",
			MatchItem{
				Path:    PathMatch{Kind: PathPrefix, Path: "/a"},
				Headers: []HeaderMatch{{Name: "x", Op: HeaderPrefix, Value: "team"}},
			},
			MatchItem{
				Path:    PathMatch{Kind: PathPrefix, Path: "/a/b"},
				Headers: []HeaderMatch{{Name: "x", Op: HeaderPrefix, Value: "team-a"}},
			},
			false, "x=team-other 满足前者不满足后者",
		},
		{
			"前者每个头条件都须被蕴含",
			MatchItem{
				Path: PathMatch{Kind: PathPrefix, Path: "/a"},
				Headers: []HeaderMatch{
					{Name: "x", Op: HeaderExact, Value: "1"},
					{Name: "y", Op: HeaderPresent},
				},
			},
			MatchItem{
				Path:    PathMatch{Kind: PathPrefix, Path: "/a/b"},
				Headers: []HeaderMatch{{Name: "x", Op: HeaderExact, Value: "1"}},
			},
			false, "后者无条件蕴含前者的 y 存在要求",
		},
		{
			"同名存在条件互相蕴含(头名大小写)",
			MatchItem{
				Path:    PathMatch{Kind: PathPrefix, Path: "/a"},
				Headers: []HeaderMatch{{Name: "Y", Op: HeaderPresent}},
			},
			MatchItem{
				Path:    PathMatch{Kind: PathPrefix, Path: "/a/b"},
				Headers: []HeaderMatch{{Name: "y", Op: HeaderPresent}},
			},
			true, "头名归一化后相同，存在蕴含存在",
		},
		{
			"一项逃逸即不遮蔽",
			MatchItem{Path: PathMatch{Kind: PathPrefix, Path: "/a"}},
			MatchItem{Path: PathMatch{Kind: PathPrefix, Path: "/c"}},
			false, "后者匹配项 /c 不被覆盖",
		},
	}
	for _, c := range cases {
		m := NewMesh()
		cfg := twoRuleConfig(c.r0, c.r1)
		_, e := m.Publish("svc", cfg, 0)
		l.publish("svc", cfg, 0, 0, e, ClassValidation)
		if c.reject {
			if e == nil || e.Kind != ValShadowed || e.RuleIdx != 1 {
				t.Fatalf("%s: 期望遮蔽拒绝 got %v", c.name, e)
			}
		} else if e != nil {
			t.Fatalf("%s: 期望发布成功 got %v", c.name, e)
		}
		l.log("判定依据: %s", c.basis)
	}
}

func TestShadowWithMultipleEarlierRules(t *testing.T) {
	l := newLogger(t)
	m := NewMesh()
	// 规则2有两个匹配项，分别被规则0、规则1各覆盖一个 => 整体被遮蔽。
	cfg := &ServiceConfig{Rules: []Rule{
		{Matches: []MatchItem{{Path: PathMatch{Kind: PathPrefix, Path: "/a"}}},
			Targets: []Target{{Subset: "a", Weight: 100}}},
		{Matches: []MatchItem{{Path: PathMatch{Kind: PathPrefix, Path: "/b"}}},
			Targets: []Target{{Subset: "b", Weight: 100}}},
		{Matches: []MatchItem{
			{Path: PathMatch{Kind: PathPrefix, Path: "/a/x"}},
			{Path: PathMatch{Kind: PathExact, Path: "/b"}},
		}, Targets: []Target{{Subset: "c", Weight: 100}}},
	}}
	_, e := m.Publish("svc", cfg, 0)
	l.publish("svc", cfg, 0, 0, e, ClassValidation)
	if e == nil || e.Kind != ValShadowed || e.RuleIdx != 2 {
		t.Fatalf("期望规则2被前面多条规则联合遮蔽, got %v", e)
	}
	l.log("判定依据: 规则2两个匹配项分别被规则0、规则1覆盖，整体永不命中")
}
