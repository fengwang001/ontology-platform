package servicemesh

import (
	"testing"
	"time"
)

func basicRule(targets []Target) []Rule {
	return []Rule{{
		Matches: []MatchItem{{Path: PathMatch{Kind: PathExact, Path: "/"}}},
		Targets: targets,
	}}
}

func TestValidationPriority(t *testing.T) {
	l := newLogger(t)
	cases := []struct {
		name    string
		cfg     *ServiceConfig
		kind    ValidationKind
		ruleIdx int
		basis   string
	}{
		{"权重和不为100",
			&ServiceConfig{Rules: basicRule([]Target{{Subset: "a", Weight: 60}})},
			ValWeight, 0, "权重和必须为100"},
		{"权重越界",
			&ServiceConfig{Rules: basicRule([]Target{{Subset: "a", Weight: 101}})},
			ValWeight, 0, "权重取值范围[0,100]"},
		{"空子集名",
			&ServiceConfig{Rules: basicRule([]Target{{Subset: "", Weight: 100}})},
			ValWeight, 0, "目标子集名称非空属分权重类"},
		{"策略报最小规则序号",
			&ServiceConfig{Rules: []Rule{
				{
					Matches:  []MatchItem{{Path: PathMatch{Kind: PathExact, Path: "/a"}}},
					Targets:  []Target{{Subset: "a", Weight: 100}},
					Override: &Policy{Timeout: dur(10 * time.Millisecond), PerAttemptTimeout: dur(20 * time.Millisecond)},
				},
				{
					Matches:  []MatchItem{{Path: PathMatch{Kind: PathExact, Path: "/b"}}},
					Targets:  []Target{{Subset: "b", Weight: 100}},
					Override: &Policy{Timeout: dur(5 * time.Millisecond), PerAttemptTimeout: dur(9 * time.Millisecond)},
				},
			}},
			ValPolicy, 0, "同类报规则序号最小者"},
		{"重试乘每次尝试超时超限",
			&ServiceConfig{Rules: []Rule{{
				Matches: []MatchItem{{Path: PathMatch{Kind: PathExact, Path: "/"}}},
				Targets: []Target{{Subset: "a", Weight: 100}},
				Override: &Policy{
					Timeout:           dur(100 * time.Millisecond),
					PerAttemptTimeout: dur(40 * time.Millisecond),
					MaxRetries:        intPtr(3),
				},
			}}},
			ValPolicy, 0, "3*40ms > 100ms"},
		{"权重先于策略",
			&ServiceConfig{Rules: []Rule{{
				Matches:  []MatchItem{{Path: PathMatch{Kind: PathExact, Path: "/"}}},
				Targets:  []Target{{Subset: "a", Weight: 99}},
				Override: &Policy{Timeout: dur(time.Millisecond), PerAttemptTimeout: dur(2 * time.Millisecond)},
			}}},
			ValWeight, 0, "分权重类先于策略类"},
		{"策略先于遮蔽",
			&ServiceConfig{Rules: []Rule{
				{
					Matches:  []MatchItem{{Path: PathMatch{Kind: PathPrefix, Path: "/a"}}},
					Targets:  []Target{{Subset: "a", Weight: 100}},
					Override: &Policy{Timeout: dur(10 * time.Millisecond), PerAttemptTimeout: dur(11 * time.Millisecond)},
				},
				{
					Matches: []MatchItem{{Path: PathMatch{Kind: PathPrefix, Path: "/a/b"}}},
					Targets: []Target{{Subset: "b", Weight: 100}},
				},
			}},
			ValPolicy, 0, "规则0策略非法优先于规则1被遮蔽"},
		{"兜底默认策略校验",
			&ServiceConfig{
				Default: Policy{Timeout: dur(5 * time.Millisecond), PerAttemptTimeout: dur(9 * time.Millisecond)},
				Rules: []Rule{{
					Matches: []MatchItem{{Path: PathMatch{Kind: PathExact, Path: "/"}}},
					Targets: []Target{{Subset: "a", Weight: 100}},
					Override: &Policy{Timeout: dur(100 * time.Millisecond),
						PerAttemptTimeout: dur(10 * time.Millisecond)},
				}},
				Fallbacks: []Target{{Subset: "fb", Weight: 100}},
			},
			ValPolicy, -1, "存在兜底时服务默认策略必须自洽"},
	}
	for _, c := range cases {
		m := NewMesh()
		_, e := m.Publish("svc", c.cfg, 0)
		l.publish("svc", c.cfg, 0, 0, e, ClassValidation)
		if e == nil || e.Class != ClassValidation || e.Kind != c.kind || e.RuleIdx != c.ruleIdx {
			t.Fatalf("%s: 期望 校验/%d/rule=%d got %v", c.name, c.kind, c.ruleIdx, e)
		}
		l.log("判定依据: %s", c.basis)
	}
}

func TestVersionConflictAndFailedPublish(t *testing.T) {
	l := newLogger(t)
	m := NewMesh()
	cfg := &ServiceConfig{Rules: basicRule([]Target{{Subset: "a", Weight: 100}})}

	v, e := m.Publish("svc", cfg, 0)
	l.publish("svc", cfg, 0, v, e, -1)
	if e != nil || v != 1 {
		t.Fatalf("首次发布版本应为1 got %d %v", v, e)
	}

	bad := &ServiceConfig{Rules: basicRule([]Target{{Subset: "x", Weight: 100}})}
	_, e = m.Publish("svc", bad, 0)
	l.publish("svc", bad, 0, 0, e, ClassVersionConflict)
	if e == nil || e.Class != ClassVersionConflict || m.Version("svc") != 1 {
		t.Fatalf("期望版本冲突且版本不变, got %v", e)
	}

	invalid := &ServiceConfig{Rules: basicRule([]Target{{Subset: "a", Weight: 99}})}
	_, e = m.Publish("svc", invalid, 1)
	l.publish("svc", invalid, 1, 0, e, ClassValidation)
	if e == nil || e.Class != ClassValidation || m.Version("svc") != 1 {
		t.Fatalf("校验失败后配置/版本必须不变, got %v", e)
	}

	v2, e := m.Publish("svc", bad, 1)
	l.publish("svc", bad, 1, v2, e, -1)
	if e != nil || v2 != 2 {
		t.Fatalf("基于当前版本应成功且版本=2 got %d %v", v2, e)
	}
}

func TestPublishedConfigImmutability(t *testing.T) {
	l := newLogger(t)
	m := NewMesh()
	cfg := &ServiceConfig{Rules: []Rule{{
		Matches:  []MatchItem{{Path: PathMatch{Kind: PathPrefix, Path: "/a"}}},
		Targets:  []Target{{Subset: "a", Weight: 100}},
		Override: &Policy{Timeout: dur(time.Second)},
	}}}
	mustPublish(t, l, m, "svc", cfg, 0)

	// 调用方修改发布入参不影响内部配置。
	cfg.Rules[0].Targets[0].Subset = "mutated"
	cfg.Rules[0].Matches[0].Path.Path = "/mutated"
	*cfg.Rules[0].Override.Timeout = 999 * time.Nanosecond
	got, ver, ok := m.GetConfig("svc")
	if !ok || ver != 1 {
		t.Fatalf("GetConfig: ok=%v ver=%d", ok, ver)
	}
	if got.Rules[0].Targets[0].Subset != "a" ||
		got.Rules[0].Matches[0].Path.Path != "/a" ||
		*got.Rules[0].Override.Timeout != time.Second {
		t.Fatalf("内部配置被调用方引用污染: %+v", got)
	}
	l.log("入参发布后被调用方篡改 => GetConfig=%+v ver=%d | 判定依据: 深拷贝隔离", got.Rules[0], ver)

	// 调用方修改读取结果也不影响内部配置。
	got.Rules[0].Targets[0].Subset = "tampered"
	again, _, _ := m.GetConfig("svc")
	if again.Rules[0].Targets[0].Subset != "a" {
		t.Fatal("GetConfig 返回结果与内部共享内存")
	}
}
