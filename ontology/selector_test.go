package ontology

import (
	"errors"
	"sync"
	"testing"
)

func mustNew(t *testing.T, sels []SelectorSpec, policy FallbackPolicy, def map[string]string, pt int) *HostSelector {
	t.Helper()
	s, err := NewHostSelector(sels, policy, def, pt)
	if err != nil {
		t.Fatalf("NewHostSelector: %v", err)
	}
	return s
}

func mustRoute(t *testing.T, s *HostSelector, labels map[string]string) string {
	t.Helper()
	id, err := s.Route(labels)
	if err != nil {
		t.Fatalf("Route(%v): %v", labels, err)
	}
	return id
}

func routeErr(t *testing.T, s *HostSelector, labels map[string]string, want error) {
	t.Helper()
	id, err := s.Route(labels)
	if !errors.Is(err, want) {
		t.Fatalf("Route(%v) = %q, %v; want err %v", labels, id, err, want)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestWorkedExample 复现题面给出的完整示例。
func TestWorkedExample(t *testing.T) {
	sels := []SelectorSpec{
		{Keys: []string{"version"}},
		{Keys: []string{"version", "zone"}, FallbackKeys: []string{"version"}},
	}
	s := mustNew(t, sels, FallbackAny, nil, 50)
	must(t, s.AddHost("a", map[string]string{"version": "v1", "zone": "z1"}, true))
	must(t, s.AddHost("b", map[string]string{"version": "v1", "zone": "z2"}, true))
	must(t, s.AddHost("c", map[string]string{"version": "v2", "zone": "z1"}, true))
	must(t, s.AddHost("d", map[string]string{"version": "v2", "zone": "z2"}, false))
	must(t, s.AddHost("e", map[string]string{"version": "v3", "zone": "z1"}, false))

	v1 := map[string]string{"version": "v1"}
	if got := mustRoute(t, s, v1); got != "a" {
		t.Fatalf("got %s want a", got)
	}
	if got := mustRoute(t, s, v1); got != "b" {
		t.Fatalf("got %s want b", got)
	}

	if got := mustRoute(t, s, map[string]string{"version": "v2"}); got != "c" {
		t.Fatalf("got %s want c", got)
	}
	// 恐慌：A=[d] H=[]，0 < 50，选中不健康的 d。
	if got := mustRoute(t, s, map[string]string{"version": "v2", "zone": "z2"}); got != "d" {
		t.Fatalf("got %s want d (panic)", got)
	}
	// 直接落空，降到 {version}，复用 {version:v1} 子集计数 2，得 a。
	if got := mustRoute(t, s, map[string]string{"version": "v1", "zone": "z9"}); got != "a" {
		t.Fatalf("got %s want a (fallback-key)", got)
	}
	// 两级都落空，全局任意端点，g=0 得 a。
	if got := mustRoute(t, s, map[string]string{"version": "v9", "zone": "z1"}); got != "a" {
		t.Fatalf("got %s want a (global)", got)
	}
	// {version} 落空、无 FK，全局回退，g=1 得 b。
	if got := mustRoute(t, s, map[string]string{"version": "v4"}); got != "b" {
		t.Fatalf("got %s want b (global)", got)
	}
}

// TestKeySetEquality 超集/子集都不算相等，走全局回退；K 为空也走回退。
func TestKeySetEquality(t *testing.T) {
	sels := []SelectorSpec{
		{Keys: []string{"version"}},
		{Keys: []string{"version", "zone"}, FallbackKeys: []string{"version"}},
	}
	s := mustNew(t, sels, FallbackAny, nil, 0)
	must(t, s.AddHost("a", map[string]string{"version": "v1", "zone": "z1"}, true))
	must(t, s.AddHost("b", map[string]string{"version": "v1", "zone": "z2", "rack": "r1"}, true))

	// 超集：{version,zone,rack} 不匹配任何选择器 -> 全局 g=0 -> a。
	if got := mustRoute(t, s, map[string]string{"version": "v1", "zone": "z1", "rack": "r9"}); got != "a" {
		t.Fatalf("superset got %s want a via global", got)
	}
	// 子集：{zone} 不匹配 -> 全局（g=1，候选 [a,b] 得 b）。
	if got := mustRoute(t, s, map[string]string{"zone": "z1"}); got != "b" {
		t.Fatalf("subset got %s want b via global", got)
	}

	// 空键集合走全局回退；不回退策略报无匹配选择器。
	s2 := mustNew(t, []SelectorSpec{{Keys: []string{"version"}}}, FallbackNone, nil, 0)
	routeErr(t, s2, map[string]string{}, ErrNoMatchingSelector)
}

// TestPanicBoundary 阈值边界：|H|*100 == Pt*|A| 不恐慌，差 1 恐慌。
func TestPanicBoundary(t *testing.T) {
	// Pt=50, |A|=2, |H|=1：100 == 100，非恐慌，只在 H 上取模。
	s := mustNew(t, []SelectorSpec{{Keys: []string{"v"}}}, FallbackNone, nil, 50)
	must(t, s.AddHost("h1", map[string]string{"v": "x"}, true))
	must(t, s.AddHost("h2", map[string]string{"v": "x"}, false))
	for i := 0; i < 4; i++ {
		if got := mustRoute(t, s, map[string]string{"v": "x"}); got != "h1" {
			t.Fatalf("equality boundary iter %d got %s want h1 (no panic)", i, got)
		}
	}

	// Pt=34, |A|=3, |H|=1：100 < 102（差 1），恐慌，在 A=[h1,h2,h3] 上取模。
	s2 := mustNew(t, []SelectorSpec{{Keys: []string{"v"}}}, FallbackNone, nil, 34)
	must(t, s2.AddHost("h1", map[string]string{"v": "x"}, true))
	must(t, s2.AddHost("h2", map[string]string{"v": "x"}, false))
	must(t, s2.AddHost("h3", map[string]string{"v": "x"}, false))
	if got := mustRoute(t, s2, map[string]string{"v": "x"}); got != "h1" {
		t.Fatalf("panic pick1 got %s want h1", got)
	}
	if got := mustRoute(t, s2, map[string]string{"v": "x"}); got != "h2" {
		t.Fatalf("panic modulo over A got %s want h2 (unhealthy)", got)
	}
	if got := mustRoute(t, s2, map[string]string{"v": "x"}); got != "h3" {
		t.Fatalf("panic modulo over A got %s want h3 (unhealthy)", got)
	}
}

// TestPanicZero A 非空 H 为空且 Pt=0 时落空并降键；无 FK 时报无健康主机。
func TestPanicZero(t *testing.T) {
	sels := []SelectorSpec{
		{Keys: []string{"version", "zone"}, FallbackKeys: []string{"version"}},
		{Keys: []string{"version"}},
	}
	s := mustNew(t, sels, FallbackNone, nil, 0)
	must(t, s.AddHost("a", map[string]string{"version": "v1", "zone": "z1"}, false))
	must(t, s.AddHost("b", map[string]string{"version": "v1", "zone": "z2"}, true))
	// 直接评估 A=[a], H=[], Pt=0 不恐慌 -> 落空；降键 {version:v1}：H=[b] -> b。
	if got := mustRoute(t, s, map[string]string{"version": "v1", "zone": "z1"}); got != "b" {
		t.Fatalf("got %s want b", got)
	}

	s2 := mustNew(t, []SelectorSpec{{Keys: []string{"version"}}}, FallbackNone, nil, 0)
	must(t, s2.AddHost("a", map[string]string{"version": "v1"}, false))
	routeErr(t, s2, map[string]string{"version": "v1"}, ErrNoHealthyHost)
}

// TestFallbackKeySharedCounter 降键与直接命中共用同一子集计数。
func TestFallbackKeySharedCounter(t *testing.T) {
	s := mustNew(t, []SelectorSpec{
		{Keys: []string{"version"}},
		{Keys: []string{"version", "zone"}, FallbackKeys: []string{"version"}},
	}, FallbackNone, nil, 0)
	must(t, s.AddHost("a", map[string]string{"version": "v1", "zone": "z1"}, true))
	must(t, s.AddHost("b", map[string]string{"version": "v1", "zone": "z2"}, true))

	mustRoute(t, s, map[string]string{"version": "v1"}) // c=1 -> a
	mustRoute(t, s, map[string]string{"version": "v1"}) // c=2 -> b
	// 直接命中不存在的 zone 落空，降键 {version:v1} 复用 c=2 -> a，推进到 3。
	if got := mustRoute(t, s, map[string]string{"version": "v1", "zone": "zz"}); got != "a" {
		t.Fatalf("shared counter got %s want a", got)
	}
	if got := mustRoute(t, s, map[string]string{"version": "v1"}); got != "b" {
		t.Fatalf("after fallback advance got %s want b (c=3)", got)
	}
}

// TestFallbackOnlyOneLevel 降键只降一级，不沿其他选择器的 FK 递归。
func TestFallbackOnlyOneLevel(t *testing.T) {
	s := mustNew(t, []SelectorSpec{
		{Keys: []string{"a", "b", "c"}, FallbackKeys: []string{"a", "b"}},
		{Keys: []string{"a", "b"}, FallbackKeys: []string{"a"}},
		{Keys: []string{"a"}},
	}, FallbackNone, nil, 0)
	must(t, s.AddHost("h", map[string]string{"a": "1"}, true)) // 只匹配 {a}
	// 若错误递归：{a,b,c} -> {a,b} -> {a} 会选中 h；正确：只评估到 {a,b}，落空 -> 报错。
	routeErr(t, s, map[string]string{"a": "1", "b": "9", "c": "9"}, ErrNoHealthyHost)

	// 直接请求 {a,b} 命中第二个选择器时，它自己的 FK={a} 生效 -> h。
	if got := mustRoute(t, s, map[string]string{"a": "1", "b": "9"}); got != "h" {
		t.Fatalf("own FK on direct hit got %s want h", got)
	}
}

// TestFallbackPolicies 三种回退策略各一例，以及回退候选为空报错。
func TestFallbackPolicies(t *testing.T) {
	// 不回退：两种错误可区分。
	s0 := mustNew(t, []SelectorSpec{{Keys: []string{"v"}}}, FallbackNone, nil, 0)
	routeErr(t, s0, map[string]string{"w": "1"}, ErrNoMatchingSelector)
	routeErr(t, s0, map[string]string{"v": "1"}, ErrNoHealthyHost)

	// 任意端点。
	s1 := mustNew(t, []SelectorSpec{{Keys: []string{"v"}}}, FallbackAny, nil, 0)
	must(t, s1.AddHost("a", map[string]string{"v": "z"}, true))
	must(t, s1.AddHost("b", map[string]string{"v": "z"}, true))
	if got := mustRoute(t, s1, map[string]string{"v": "nope"}); got != "a" {
		t.Fatalf("any got %s want a", got)
	}
	must(t, s1.RemoveHost("a"))
	must(t, s1.RemoveHost("b"))
	routeErr(t, s1, map[string]string{"v": "nope"}, ErrNoHealthyHost)

	// 默认子集：默认候选与全局健康集合不同。
	s2 := mustNew(t, []SelectorSpec{{Keys: []string{"v"}}}, FallbackDefaultSubset,
		map[string]string{"tier": "gold"}, 0)
	must(t, s2.AddHost("a", map[string]string{"v": "z", "tier": "gold"}, true))
	must(t, s2.AddHost("b", map[string]string{"v": "z", "tier": "silver"}, true))
	if got := mustRoute(t, s2, map[string]string{"v": "nope"}); got != "a" {
		t.Fatalf("default subset got %s want a", got)
	}
	if got := mustRoute(t, s2, map[string]string{"w": "other"}); got != "a" {
		t.Fatalf("default subset on no selector got %s want a", got)
	}
	must(t, s2.SetHealth("a", false))
	routeErr(t, s2, map[string]string{"v": "nope"}, ErrNoHealthyHost)
}

// TestIndependentCounters 各子集计数独立且只在成功时推进。
func TestIndependentCounters(t *testing.T) {
	s := mustNew(t, []SelectorSpec{{Keys: []string{"v"}}}, FallbackNone, nil, 0)
	must(t, s.AddHost("a1", map[string]string{"v": "a"}, true))
	must(t, s.AddHost("a2", map[string]string{"v": "a"}, true))
	must(t, s.AddHost("b1", map[string]string{"v": "b"}, true))

	mustRoute(t, s, map[string]string{"v": "a"}) // a: c 0->1
	mustRoute(t, s, map[string]string{"v": "a"}) // a: c 1->2
	// b 子集独立，c=0 选 b1。
	if got := mustRoute(t, s, map[string]string{"v": "b"}); got != "b1" {
		t.Fatalf("independent subset got %s want b1", got)
	}
	// a 落空（无匹配值）不推进任何计数；随后仍从 c=2 选 a2。
	routeErr(t, s, map[string]string{"v": "zz"}, ErrNoHealthyHost)
	routeErr(t, s, map[string]string{"v": "zz"}, ErrNoHealthyHost)
	if got := mustRoute(t, s, map[string]string{"v": "a"}); got != "a1" {
		t.Fatalf("failed evaluation must not advance, got %s want a1 (c stays 2)", got)
	}
}

// TestModuloWithChangingCandidates 候选集合变化后按当前大小取模。
func TestModuloWithChangingCandidates(t *testing.T) {
	s := mustNew(t, []SelectorSpec{{Keys: []string{"v"}}}, FallbackNone, nil, 0)
	must(t, s.AddHost("a", map[string]string{"v": "x"}, true))
	must(t, s.AddHost("b", map[string]string{"v": "x"}, true))
	must(t, s.AddHost("c", map[string]string{"v": "x"}, true))
	mustRoute(t, s, map[string]string{"v": "x"}) // c=1
	mustRoute(t, s, map[string]string{"v": "x"}) // c=2
	mustRoute(t, s, map[string]string{"v": "x"}) // c=3
	must(t, s.RemoveHost("c"))
	// c=3，当前候选 [a,b]：3%2=1 -> b。
	if got := mustRoute(t, s, map[string]string{"v": "x"}); got != "b" {
		t.Fatalf("modulo after shrink got %s want b", got)
	}
	must(t, s.AddHost("c", map[string]string{"v": "x"}, true))
	// c=4，候选 [a,b,c]：4%3=1 -> b。
	if got := mustRoute(t, s, map[string]string{"v": "x"}); got != "b" {
		t.Fatalf("modulo after grow got %s want b", got)
	}
}

// TestExactValueAndEmptyStringValue 标签值精确相等，空串值必须参与匹配。
func TestExactValueAndEmptyStringValue(t *testing.T) {
	s := mustNew(t, []SelectorSpec{{Keys: []string{"v"}}}, FallbackNone, nil, 0)
	must(t, s.AddHost("a", map[string]string{"v": ""}, true))
	must(t, s.AddHost("b", map[string]string{"v": "x"}, true))
	// 请求 v="" 必须只精确匹配 a，而不是“缺键也算”。
	if got := mustRoute(t, s, map[string]string{"v": ""}); got != "a" {
		t.Fatalf("empty-string value got %s want a", got)
	}
	// v="x" 精确匹配 b。
	if got := mustRoute(t, s, map[string]string{"v": "x"}); got != "b" {
		t.Fatalf("exact value got %s want b", got)
	}
	// 主机缺键不参与匹配。
	s2 := mustNew(t, []SelectorSpec{{Keys: []string{"v", "z"}}}, FallbackNone, nil, 0)
	must(t, s2.AddHost("a", map[string]string{"v": "1"}, true)) // 缺 z
	must(t, s2.AddHost("b", map[string]string{"v": "1", "z": ""}, true))
	if got := mustRoute(t, s2, map[string]string{"v": "1", "z": ""}); got != "b" {
		t.Fatalf("missing key must not match, got %s want b", got)
	}
}

// TestConfigValidation 全部非法配置在构造时整体拒绝。
func TestConfigValidation(t *testing.T) {
	bad := []struct {
		name string
		sels []SelectorSpec
		pol  FallbackPolicy
		def  map[string]string
		pt   int
	}{
		{"empty selectors", nil, FallbackNone, nil, 0},
		{"empty key set", []SelectorSpec{{Keys: nil}}, FallbackNone, nil, 0},
		{"empty key", []SelectorSpec{{Keys: []string{""}}}, FallbackNone, nil, 0},
		{"dup key", []SelectorSpec{{Keys: []string{"a", "a"}}}, FallbackNone, nil, 0},
		{"dup key set", []SelectorSpec{{Keys: []string{"a"}}, {Keys: []string{"a"}}}, FallbackNone, nil, 0},
		{"fk empty key", []SelectorSpec{{Keys: []string{"a", "b"}, FallbackKeys: []string{""}}}, FallbackNone, nil, 0},
		{"fk dup", []SelectorSpec{{Keys: []string{"a", "b"}, FallbackKeys: []string{"a", "a"}}}, FallbackNone, nil, 0},
		{"fk not subset", []SelectorSpec{{Keys: []string{"a"}, FallbackKeys: []string{"b"}}}, FallbackNone, nil, 0},
		{"fk equal set", []SelectorSpec{{Keys: []string{"a"}, FallbackKeys: []string{"a"}}}, FallbackNone, nil, 0},
		{"bad policy", []SelectorSpec{{Keys: []string{"a"}}}, FallbackPolicy(99), nil, 0},
		{"pt negative", []SelectorSpec{{Keys: []string{"a"}}}, FallbackNone, nil, -1},
		{"pt over 100", []SelectorSpec{{Keys: []string{"a"}}}, FallbackNone, nil, 101},
		{"default nil", []SelectorSpec{{Keys: []string{"a"}}}, FallbackDefaultSubset, nil, 0},
		{"default empty", []SelectorSpec{{Keys: []string{"a"}}}, FallbackDefaultSubset, map[string]string{}, 0},
		{"default empty key", []SelectorSpec{{Keys: []string{"a"}}}, FallbackDefaultSubset, map[string]string{"": "x"}, 0},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewHostSelector(tc.sels, tc.pol, tc.def, tc.pt); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("want ErrInvalidConfig, got %v", err)
			}
		})
	}

	// 非默认子集策略忽略默认标签（即使为 nil/含空键也合法）。
	if _, err := NewHostSelector([]SelectorSpec{{Keys: []string{"a"}}}, FallbackNone, map[string]string{"": "x"}, 0); err != nil {
		t.Fatalf("default labels must be ignored for non-default policy: %v", err)
	}
}

// TestHostOpsRejections 登记/变更拒绝原因可区分，且拒绝不改状态。
func TestHostOpsRejections(t *testing.T) {
	s := mustNew(t, []SelectorSpec{{Keys: []string{"a"}}}, FallbackNone, nil, 0)

	// id 为空优先于空键。
	if err := s.AddHost("", map[string]string{"": "x"}, true); !errors.Is(err, ErrEmptyID) {
		t.Fatalf("want ErrEmptyID, got %v", err)
	}
	if err := s.AddHost("h", map[string]string{"": "x"}, true); !errors.Is(err, ErrEmptyLabelKey) {
		t.Fatalf("want ErrEmptyLabelKey, got %v", err)
	}
	must(t, s.AddHost("h", map[string]string{"a": "1"}, true))
	if err := s.AddHost("h", map[string]string{"a": "2"}, false); !errors.Is(err, ErrHostExists) {
		t.Fatalf("want ErrHostExists, got %v", err)
	}
	// 被拒绝的 AddHost（重复且 labels 不同）不得覆盖原主机：路由仍健康。
	if got := mustRoute(t, s, map[string]string{"a": "1"}); got != "h" {
		t.Fatalf("rejected AddHost must not mutate state, got %s", got)
	}

	if err := s.SetHealth("nope", true); !errors.Is(err, ErrHostNotFound) {
		t.Fatalf("want ErrHostNotFound, got %v", err)
	}
	if err := s.RemoveHost("nope"); !errors.Is(err, ErrHostNotFound) {
		t.Fatalf("want ErrHostNotFound, got %v", err)
	}

	// 设成相同健康值也接受。
	if err := s.SetHealth("h", true); err != nil {
		t.Fatalf("same health value: %v", err)
	}

	// 请求标签含空键。
	routeErr(t, s, map[string]string{"a": "1", "": "x"}, ErrInvalidLabels)

	// 路由失败不推进计数：先失败一次，再成功应仍得到 h（c=0）。
	s2 := mustNew(t, []SelectorSpec{{Keys: []string{"a"}}}, FallbackNone, nil, 0)
	must(t, s2.AddHost("h", map[string]string{"a": "1"}, true))
	routeErr(t, s2, map[string]string{"a": "2"}, ErrNoHealthyHost)
	if got := mustRoute(t, s2, map[string]string{"a": "1"}); got != "h" {
		t.Fatalf("failed route must not advance counter, got %s want h", got)
	}
}

// TestPanicHostSatisfiesLabels 恐慌选中的主机一定满足所用子集的标签条件。
func TestPanicHostSatisfiesLabels(t *testing.T) {
	s := mustNew(t, []SelectorSpec{{Keys: []string{"v"}}}, FallbackNone, nil, 100)
	must(t, s.AddHost("a", map[string]string{"v": "x"}, false))
	must(t, s.AddHost("b", map[string]string{"v": "x"}, false))
	must(t, s.AddHost("c", map[string]string{"v": "y"}, false))
	for i := 0; i < 6; i++ {
		got := mustRoute(t, s, map[string]string{"v": "x"})
		if got != "a" && got != "b" {
			t.Fatalf("panic picked %s which does not satisfy subset", got)
		}
	}
}

// TestConcurrent 并发登记/变更/路由可串行化（配合 -race 检查数据竞争）。
func TestConcurrent(t *testing.T) {
	s := mustNew(t, []SelectorSpec{{Keys: []string{"v"}}}, FallbackAny, nil, 50)
	for i := 0; i < 20; i++ {
		id := "h" + string(rune('a'+i%26)) + string(rune('0'+i/26))
		must(t, s.AddHost(id, map[string]string{"v": "x"}, i%2 == 0))
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				switch i % 4 {
				case 0:
					_ = s.SetHealth("ha0", i%2 == 0)
				case 1:
					_, _ = s.Route(map[string]string{"v": "x"})
				case 2:
					_, _ = s.Route(map[string]string{"v": "zz"})
				case 3:
					_ = s.RemoveHost("ha0")
					_ = s.AddHost("ha0", map[string]string{"v": "x"}, true)
				}
			}
		}(g)
	}
	wg.Wait()
}
